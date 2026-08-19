package stockv2

import (
	"strings"
	"testing"
)

func TestValidateModelHorizonOutlooks(t *testing.T) {
	valid := testModelHorizonOutlooks(10)
	if err := validateModelHorizonOutlooks(valid); err != nil {
		t.Fatalf("valid outlooks rejected: %v", err)
	}

	duplicate := append([]ModelHorizonOutlook(nil), valid...)
	duplicate[2].Horizon = ModelHorizonMedium
	duplicate[2].TradingDays = 20
	if err := validateModelHorizonOutlooks(duplicate); err == nil {
		t.Fatal("duplicate horizon accepted")
	}

	invalidRange := append([]ModelHorizonOutlook(nil), valid...)
	invalidRange[0].ExpectedPrice = invalidRange[0].RangeHigh + 1
	if err := validateModelHorizonOutlooks(invalidRange); err == nil {
		t.Fatal("expected price outside range accepted")
	}

	incomplete := append([]ModelHorizonOutlook(nil), valid...)
	incomplete[0].Direction = "neutral_to_bullish"
	incomplete[0].ExpectedPrice = 0
	incomplete[0].RangeLow = 0
	incomplete[0].TargetPrice = 0
	incomplete[0].InvalidConditions = nil
	err := validateModelHorizonOutlooks(incomplete)
	if err == nil {
		t.Fatal("incomplete outlook accepted")
	}
	for _, want := range []string{"direction (must be bullish, neutral, or bearish)", "expectedPrice", "rangeLow", "targetPrice", "invalidConditions"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("validation error %q missing %q", err, want)
		}
	}
}

func TestOpportunityCandidateHorizonOutlooksMetadataRoundTrip(t *testing.T) {
	want := testModelHorizonOutlooks(10)
	metadata := opportunityCandidateMetadataWithHorizonOutlooks(map[string]any{"source": "test"}, want)
	got := modelHorizonOutlooksFromMetadata(metadata)
	if len(got) != 3 || got[0].Horizon != ModelHorizonShort || got[2].Horizon != ModelHorizonLong {
		t.Fatalf("round trip = %#v", got)
	}
}

func testModelHorizonOutlooks(price float64) []ModelHorizonOutlook {
	return []ModelHorizonOutlook{
		{
			Horizon: ModelHorizonShort, TradingDays: 5, AsOfPrice: price, Direction: ModelOutlookBullish,
			ProbabilityUp: 0.68, ProbabilityOutperform: 0.61, ExpectedPrice: price * 1.05,
			ExpectedReturnPct: 5, RangeLow: price * 0.95, RangeHigh: price * 1.12,
			TargetPrice: price * 1.1, TargetProbability: 0.42, DownsideRiskPct: 6, Confidence: 0.7,
			Thesis: "短期测试判断", InvalidConditions: []string{"跌破测试风险位"}, DataQuality: ModelOutlookDataHealthy,
		},
		{
			Horizon: ModelHorizonMedium, TradingDays: 20, AsOfPrice: price, Direction: ModelOutlookBullish,
			ProbabilityUp: 0.64, ProbabilityOutperform: 0.58, ExpectedPrice: price * 1.08,
			ExpectedReturnPct: 8, RangeLow: price * 0.9, RangeHigh: price * 1.2,
			TargetPrice: price * 1.15, TargetProbability: 0.45, DownsideRiskPct: 10, Confidence: 0.66,
			Thesis: "中期测试判断", InvalidConditions: []string{"中期逻辑失效"}, DataQuality: ModelOutlookDataHealthy,
		},
		{
			Horizon: ModelHorizonLong, TradingDays: 60, AsOfPrice: price, Direction: ModelOutlookNeutral,
			ProbabilityUp: 0.55, ProbabilityOutperform: 0.51, ExpectedPrice: price * 1.06,
			ExpectedReturnPct: 6, RangeLow: price * 0.85, RangeHigh: price * 1.3,
			TargetPrice: price * 1.2, TargetProbability: 0.4, DownsideRiskPct: 15, Confidence: 0.58,
			Thesis: "长期测试判断", InvalidConditions: []string{"长期逻辑失效"},
			Uncertainties: []string{"长期不确定性"}, DataQuality: ModelOutlookDataDegraded,
		},
	}
}

func testModelPortfolioHorizonOutlooks() []ModelPortfolioHorizonOutlook {
	return []ModelPortfolioHorizonOutlook{
		{Horizon: ModelHorizonShort, TradingDays: 5, Direction: ModelOutlookBullish, ProbabilityGain: 0.64, ProbabilityOutperform: 0.57, ExpectedReturnPct: 2.5, RangeLowReturnPct: -3, RangeHighReturnPct: 7, ExpectedMaxDrawdownPct: 5, Confidence: 0.68, Summary: "短期组合判断", InvalidConditions: []string{"短期组合逻辑失效"}, DataQuality: ModelOutlookDataHealthy},
		{Horizon: ModelHorizonMedium, TradingDays: 20, Direction: ModelOutlookNeutral, ProbabilityGain: 0.58, ProbabilityOutperform: 0.52, ExpectedReturnPct: 3.5, RangeLowReturnPct: -7, RangeHighReturnPct: 12, ExpectedMaxDrawdownPct: 9, Confidence: 0.62, Summary: "中期组合判断", InvalidConditions: []string{"中期组合逻辑失效"}, DataQuality: ModelOutlookDataHealthy},
		{Horizon: ModelHorizonLong, TradingDays: 60, Direction: ModelOutlookNeutral, ProbabilityGain: 0.55, ProbabilityOutperform: 0.5, ExpectedReturnPct: 5, RangeLowReturnPct: -12, RangeHighReturnPct: 18, ExpectedMaxDrawdownPct: 15, Confidence: 0.55, Summary: "长期组合判断", InvalidConditions: []string{"长期组合逻辑失效"}, Uncertainties: []string{"长期不确定性"}, DataQuality: ModelOutlookDataDegraded},
	}
}
