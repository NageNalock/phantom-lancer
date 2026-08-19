package stockv2

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"

	"phantom-lancer/internal/safelog"
)

const (
	ModelHorizonShort  = "short"
	ModelHorizonMedium = "medium"
	ModelHorizonLong   = "long"

	ModelOutlookBullish = "bullish"
	ModelOutlookNeutral = "neutral"
	ModelOutlookBearish = "bearish"

	ModelOutlookDataHealthy      = "healthy"
	ModelOutlookDataDegraded     = "degraded"
	ModelOutlookDataInsufficient = "insufficient"

	opportunityCandidateHorizonOutlooksMetadataKey = "modelHorizonOutlooks"
)

var errInvalidModelHorizonOutlooks = errors.New("invalid model horizon outlooks")

// ModelHorizonOutlook is the model's conditional price forecast. Probability
// fields are model estimates, not deterministic or empirically calibrated odds.
type ModelHorizonOutlook struct {
	Horizon               string   `json:"horizon"`
	TradingDays           int      `json:"tradingDays"`
	AsOfPrice             float64  `json:"asOfPrice"`
	Direction             string   `json:"direction"`
	ProbabilityUp         float64  `json:"probabilityUp"`
	ProbabilityOutperform float64  `json:"probabilityOutperform"`
	ExpectedPrice         float64  `json:"expectedPrice"`
	ExpectedReturnPct     float64  `json:"expectedReturnPct"`
	RangeLow              float64  `json:"rangeLow"`
	RangeHigh             float64  `json:"rangeHigh"`
	TargetPrice           float64  `json:"targetPrice"`
	TargetProbability     float64  `json:"targetProbability"`
	DownsideRiskPct       float64  `json:"downsideRiskPct"`
	Confidence            float64  `json:"confidence"`
	Thesis                string   `json:"thesis"`
	InvalidConditions     []string `json:"invalidConditions"`
	Uncertainties         []string `json:"uncertainties"`
	DataQuality           string   `json:"dataQuality"`
}

type ModelPortfolioHorizonOutlook struct {
	Horizon                string   `json:"horizon"`
	TradingDays            int      `json:"tradingDays"`
	Direction              string   `json:"direction"`
	ProbabilityGain        float64  `json:"probabilityGain"`
	ProbabilityOutperform  float64  `json:"probabilityOutperform"`
	ExpectedReturnPct      float64  `json:"expectedReturnPct"`
	RangeLowReturnPct      float64  `json:"rangeLowReturnPct"`
	RangeHighReturnPct     float64  `json:"rangeHighReturnPct"`
	ExpectedMaxDrawdownPct float64  `json:"expectedMaxDrawdownPct"`
	Confidence             float64  `json:"confidence"`
	Summary                string   `json:"summary"`
	InvalidConditions      []string `json:"invalidConditions"`
	Uncertainties          []string `json:"uncertainties"`
	DataQuality            string   `json:"dataQuality"`
}

func modelHorizonTradingDays(horizon string) (int, bool) {
	switch strings.TrimSpace(horizon) {
	case ModelHorizonShort:
		return 5, true
	case ModelHorizonMedium:
		return 20, true
	case ModelHorizonLong:
		return 60, true
	default:
		return 0, false
	}
}

func validateModelHorizonOutlooks(items []ModelHorizonOutlook) error {
	if len(items) != 3 {
		return fmt.Errorf("%w: exactly short, medium, and long forecasts are required", errInvalidModelHorizonOutlooks)
	}
	seen := map[string]bool{}
	for index, item := range items {
		days, ok := modelHorizonTradingDays(item.Horizon)
		if !ok || seen[item.Horizon] || item.TradingDays != days {
			return fmt.Errorf("%w: item %d has an invalid or duplicate horizon", errInvalidModelHorizonOutlooks, index)
		}
		seen[item.Horizon] = true
		invalidFields := invalidModelHorizonOutlookFields(item)
		if len(invalidFields) > 0 {
			return fmt.Errorf("%w: item %d (%s/%d) invalid fields: %s", errInvalidModelHorizonOutlooks, index, item.Horizon, item.TradingDays, strings.Join(invalidFields, ", "))
		}
	}
	return nil
}

func invalidModelHorizonOutlookFields(item ModelHorizonOutlook) []string {
	fields := make([]string, 0, 16)
	if !validModelOutlookDirection(item.Direction) {
		fields = append(fields, "direction (must be bullish, neutral, or bearish)")
	}
	if !validModelOutlookDataQuality(item.DataQuality) {
		fields = append(fields, "dataQuality (must be healthy, degraded, or insufficient)")
	}
	for name, value := range map[string]float64{
		"probabilityUp": item.ProbabilityUp, "probabilityOutperform": item.ProbabilityOutperform,
		"targetProbability": item.TargetProbability, "confidence": item.Confidence,
	} {
		if !validProbability(value) {
			fields = append(fields, name+" (must be 0..1)")
		}
	}
	for name, value := range map[string]float64{
		"asOfPrice": item.AsOfPrice, "expectedPrice": item.ExpectedPrice, "rangeLow": item.RangeLow,
		"rangeHigh": item.RangeHigh, "targetPrice": item.TargetPrice,
	} {
		if !finitePositive(value) {
			fields = append(fields, name+" (must be > 0 and cannot be omitted)")
		}
	}
	if finitePositive(item.RangeLow) && finitePositive(item.ExpectedPrice) && finitePositive(item.RangeHigh) &&
		(item.RangeLow > item.ExpectedPrice || item.ExpectedPrice > item.RangeHigh) {
		fields = append(fields, "rangeLow <= expectedPrice <= rangeHigh")
	}
	if !finiteNumber(item.ExpectedReturnPct) {
		fields = append(fields, "expectedReturnPct (must be numeric)")
	}
	if !finitePercentage(item.DownsideRiskPct) {
		fields = append(fields, "downsideRiskPct (must be 0..100)")
	}
	if strings.TrimSpace(item.Thesis) == "" {
		fields = append(fields, "thesis (required)")
	}
	if len(compactStrings(item.InvalidConditions)) == 0 {
		fields = append(fields, "invalidConditions (at least one item required)")
	}
	return fields
}

func validateModelPortfolioHorizonOutlooks(items []ModelPortfolioHorizonOutlook) error {
	if len(items) != 3 {
		return fmt.Errorf("%w: exactly short, medium, and long portfolio forecasts are required", errInvalidModelHorizonOutlooks)
	}
	seen := map[string]bool{}
	for index, item := range items {
		days, ok := modelHorizonTradingDays(item.Horizon)
		if !ok || seen[item.Horizon] || item.TradingDays != days {
			return fmt.Errorf("%w: portfolio item %d has an invalid or duplicate horizon", errInvalidModelHorizonOutlooks, index)
		}
		seen[item.Horizon] = true
		if !validModelOutlookDirection(item.Direction) || !validModelOutlookDataQuality(item.DataQuality) ||
			!validProbability(item.ProbabilityGain) || !validProbability(item.ProbabilityOutperform) || !validProbability(item.Confidence) ||
			!finiteNumber(item.ExpectedReturnPct) || !finiteNumber(item.RangeLowReturnPct) || !finiteNumber(item.RangeHighReturnPct) ||
			item.RangeLowReturnPct > item.ExpectedReturnPct || item.ExpectedReturnPct > item.RangeHighReturnPct ||
			!finitePercentage(item.ExpectedMaxDrawdownPct) || strings.TrimSpace(item.Summary) == "" ||
			len(compactStrings(item.InvalidConditions)) == 0 {
			return fmt.Errorf("%w: portfolio item %d has invalid fields", errInvalidModelHorizonOutlooks, index)
		}
	}
	return nil
}

func validModelOutlookDirection(value string) bool {
	switch strings.TrimSpace(value) {
	case ModelOutlookBullish, ModelOutlookNeutral, ModelOutlookBearish:
		return true
	default:
		return false
	}
}

func validModelOutlookDataQuality(value string) bool {
	switch strings.TrimSpace(value) {
	case ModelOutlookDataHealthy, ModelOutlookDataDegraded, ModelOutlookDataInsufficient:
		return true
	default:
		return false
	}
}

func validProbability(value float64) bool {
	return finiteNumber(value) && value >= 0 && value <= 1
}

func finitePercentage(value float64) bool {
	return finiteNumber(value) && value >= 0 && value <= 100
}

func finitePositive(value float64) bool {
	return finiteNumber(value) && value > 0
}

func finiteNumber(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func sanitizeModelHorizonOutlooks(items []ModelHorizonOutlook) []ModelHorizonOutlook {
	out := append([]ModelHorizonOutlook(nil), items...)
	for index := range out {
		out[index].Horizon = strings.TrimSpace(out[index].Horizon)
		out[index].Direction = strings.TrimSpace(out[index].Direction)
		out[index].Thesis = safelog.Text(out[index].Thesis, 1200)
		out[index].InvalidConditions = sanitizeModelOutlookStrings(out[index].InvalidConditions)
		out[index].Uncertainties = sanitizeModelOutlookStrings(out[index].Uncertainties)
		out[index].DataQuality = strings.TrimSpace(out[index].DataQuality)
	}
	return out
}

func sanitizeModelPortfolioHorizonOutlooks(items []ModelPortfolioHorizonOutlook) []ModelPortfolioHorizonOutlook {
	out := append([]ModelPortfolioHorizonOutlook(nil), items...)
	for index := range out {
		out[index].Horizon = strings.TrimSpace(out[index].Horizon)
		out[index].Direction = strings.TrimSpace(out[index].Direction)
		out[index].Summary = safelog.Text(out[index].Summary, 1200)
		out[index].InvalidConditions = sanitizeModelOutlookStrings(out[index].InvalidConditions)
		out[index].Uncertainties = sanitizeModelOutlookStrings(out[index].Uncertainties)
		out[index].DataQuality = strings.TrimSpace(out[index].DataQuality)
	}
	return out
}

func sanitizeModelOutlookStrings(items []string) []string {
	items = compactStrings(items)
	if len(items) > 8 {
		items = items[:8]
	}
	for index := range items {
		items[index] = safelog.Text(items[index], 600)
	}
	return items
}

func opportunityCandidateMetadataWithHorizonOutlooks(metadata map[string]any, items []ModelHorizonOutlook) map[string]any {
	if metadata == nil {
		metadata = map[string]any{}
	}
	if len(items) == 0 {
		delete(metadata, opportunityCandidateHorizonOutlooksMetadataKey)
		return metadata
	}
	metadata[opportunityCandidateHorizonOutlooksMetadataKey] = sanitizeModelHorizonOutlooks(items)
	return metadata
}

func modelHorizonOutlooksFromMetadata(metadata map[string]any) []ModelHorizonOutlook {
	value, ok := metadata[opportunityCandidateHorizonOutlooksMetadataKey]
	if !ok {
		return nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var items []ModelHorizonOutlook
	if json.Unmarshal(raw, &items) != nil || validateModelHorizonOutlooks(items) != nil {
		return nil
	}
	return sanitizeModelHorizonOutlooks(items)
}
