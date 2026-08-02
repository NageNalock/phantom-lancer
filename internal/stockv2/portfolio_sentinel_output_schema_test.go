package stockv2

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestPortfolioSentinelDirectOutputSchemaConstrainsResultCollections(t *testing.T) {
	raw, err := portfolioSentinelDirectOutputSchema("task-sentinel")
	if err != nil {
		t.Fatalf("build schema: %v", err)
	}
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("decode schema: %v", err)
	}
	properties := schema["properties"].(map[string]any)
	if properties["taskID"].(map[string]any)["const"] != "task-sentinel" {
		t.Fatalf("taskID schema = %#v", properties["taskID"])
	}
	result := properties["result"].(map[string]any)["properties"].(map[string]any)["result"].(map[string]any)
	reportProperties := result["properties"].(map[string]any)
	for _, field := range []string{"negative_items", "review_requests", "action_plans"} {
		fieldSchema := reportProperties[field].(map[string]any)
		if fieldSchema["type"] != "array" || fieldSchema["items"].(map[string]any)["type"] != "object" {
			t.Fatalf("%s schema = %#v", field, fieldSchema)
		}
	}
}

func TestPortfolioSentinelReportKeepsPlansWhenFreeFormItemsAreStrings(t *testing.T) {
	raw := []byte(`{
		"schema_version":"portfolio-sentinel-report/v2",
		"overall_risk_level":"medium",
		"run_summary":"complete",
		"negative_items":["sector volatility"],
		"action_plans":[
			{"id":"plan-1","portfolio_id":"portfolio-1","symbol":"000977","action":"hold","trigger_mode":"immediate","reason":"wait"},
			{"id":"plan-2","portfolio_id":"portfolio-1","symbol":"588940","action":"hold","trigger_mode":"immediate","reason":"wait"}
		],
		"review_requests":[]
	}`)
	var report PortfolioSentinelReport
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	if len(report.ActionPlans) != 2 || report.ActionPlans[0].Symbol != "000977" || report.ActionPlans[1].Symbol != "588940" {
		t.Fatalf("action plans were lost: %#v", report.ActionPlans)
	}
	if len(report.NegativeItems) != 1 {
		t.Fatalf("negative items = %#v", report.NegativeItems)
	}
}

func TestPortfolioSentinelReportRejectsStringReviewRequest(t *testing.T) {
	raw := []byte(`{
		"schema_version":"portfolio-sentinel-report/v2",
		"overall_risk_level":"medium",
		"run_summary":"complete",
		"review_requests":["review later"]
	}`)
	var report PortfolioSentinelReport
	err := json.Unmarshal(raw, &report)
	if err == nil || !strings.Contains(err.Error(), "cannot unmarshal string") {
		t.Fatalf("err = %v, want typed review request failure", err)
	}
}

func TestPortfolioSentinelDecodeFailureIsARecoverableModelResult(t *testing.T) {
	_, err := portfolioSentinelReportFromResult(map[string]any{
		"schema_version":     PortfolioSentinelReportSchemaVersion,
		"overall_risk_level": PortfolioSentinelRiskMedium,
		"run_summary":        "complete",
		"review_requests":    []any{"review later"},
	})
	if !errors.Is(err, ErrInvalidPortfolioSentinelResult) {
		t.Fatalf("err = %v, want ErrInvalidPortfolioSentinelResult", err)
	}
	if !portfolioSentinelFallbackEligible(
		context.Background(),
		AgentRun{Status: AgentRunStatusFailed, ErrorMessage: err.Error()},
		nil,
		nil,
	) {
		t.Fatal("invalid model output should allow one fallback attempt")
	}
}
