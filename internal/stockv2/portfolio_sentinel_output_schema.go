package stockv2

import (
	"encoding/json"
	"fmt"
)

func portfolioSentinelDirectOutputSchema(taskID string) ([]byte, error) {
	stringArray := func() map[string]any {
		return map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
	}
	objectArray := func() map[string]any {
		return map[string]any{"type": "array", "items": map[string]any{"type": "object"}}
	}
	condition := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"key", "type"},
		"properties": map[string]any{
			"key": map[string]any{"type": "string"},
			"type": map[string]any{"type": "string", "enum": []string{
				WatchRulePriceAbove, WatchRulePriceBelow, WatchRulePriceBetween,
				WatchRulePctChangeAbove, WatchRulePctChangeBelow,
				WatchRuleDailyCloseAbove, WatchRuleDailyCloseBelow,
				WatchRulePortfolioSymbolWeightOver, WatchRulePortfolioSymbolWeightBelow,
			}},
			"threshold": map[string]any{"type": "number"},
			"low":       map[string]any{"type": "number"},
			"high":      map[string]any{"type": "number"},
		},
	}
	actionPlan := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"id", "portfolio_id", "symbol", "action", "trigger_mode", "reason"},
		"properties": map[string]any{
			"id":           map[string]any{"type": "string"},
			"portfolio_id": map[string]any{"type": "string"},
			"symbol":       map[string]any{"type": "string"},
			"market":       map[string]any{"type": "string"},
			"name":         map[string]any{"type": "string"},
			"action": map[string]any{"type": "string", "enum": []string{
				PortfolioSentinelPlanBuild, PortfolioSentinelPlanAdd, PortfolioSentinelPlanHold,
				PortfolioSentinelPlanReduce, PortfolioSentinelPlanExit,
			}},
			"trigger_mode":   map[string]any{"type": "string", "enum": []string{PortfolioSentinelTriggerImmediate, PortfolioSentinelTriggerConditional}},
			"trigger_policy": map[string]any{"type": "string", "enum": []string{WatchTriggerPolicyAll, WatchTriggerPolicyAny}},
			"conditions":     map[string]any{"type": "array", "maxItems": 10, "items": condition},
			"sizing": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"required":             []string{"mode", "value"},
				"properties": map[string]any{
					"mode":  map[string]any{"type": "string", "enum": []string{PortfolioSentinelSizingAvailableQuantityPct, PortfolioSentinelSizingTargetPortfolioPct}},
					"value": map[string]any{"type": "number", "exclusiveMinimum": 0, "maximum": 100},
				},
			},
			"reason":        map[string]any{"type": "string"},
			"risk_notes":    map[string]any{"type": "string"},
			"confidence":    map[string]any{"type": "number", "minimum": 0, "maximum": 1},
			"evidence_refs": stringArray(),
			"research_refs": stringArray(),
		},
	}
	report := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required": []string{
			"schema_version", "overall_risk_level", "run_summary",
			"positive_items", "negative_items", "noise_items", "affected_holdings",
			"action_plans", "research_audit", "review_requests", "data_quality_notes",
			"next_watch_focus", "checked_news_thread_version_ids",
		},
		"properties": map[string]any{
			"schema_version":     map[string]any{"type": "string", "const": PortfolioSentinelReportSchemaVersion},
			"overall_risk_level": map[string]any{"type": "string", "enum": []string{PortfolioSentinelRiskLow, PortfolioSentinelRiskMedium, PortfolioSentinelRiskHigh, PortfolioSentinelRiskCritical}},
			"run_summary":        map[string]any{"type": "string"},
			"positive_items":     objectArray(),
			"negative_items":     objectArray(),
			"noise_items":        objectArray(),
			"affected_holdings": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"required":             []string{"symbol"},
					"properties": map[string]any{
						"symbol":        map[string]any{"type": "string"},
						"market":        map[string]any{"type": "string"},
						"name":          map[string]any{"type": "string"},
						"risk_level":    map[string]any{"type": "string", "enum": []string{PortfolioSentinelRiskLow, PortfolioSentinelRiskMedium, PortfolioSentinelRiskHigh, PortfolioSentinelRiskCritical}},
						"direction":     map[string]any{"type": "string", "enum": []string{"positive", "negative", "neutral"}},
						"reasons":       stringArray(),
						"evidence_refs": stringArray(),
					},
				},
			},
			"action_plans": map[string]any{"type": "array", "items": actionPlan},
			"research_audit": map[string]any{
				"type":     "array",
				"maxItems": 100,
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"required":             []string{"id", "kind", "source", "claim"},
					"properties": map[string]any{
						"id":           map[string]any{"type": "string"},
						"kind":         map[string]any{"type": "string"},
						"query":        map[string]any{"type": "string"},
						"source":       map[string]any{"type": "string"},
						"source_title": map[string]any{"type": "string"},
						"published_at": map[string]any{"type": "string"},
						"checked_at":   map[string]any{"type": "string"},
						"claim":        map[string]any{"type": "string"},
					},
				},
			},
			"review_requests": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"required":             []string{"symbol"},
					"properties": map[string]any{
						"symbol":        map[string]any{"type": "string"},
						"market":        map[string]any{"type": "string"},
						"portfolio_id":  map[string]any{"type": "string"},
						"title":         map[string]any{"type": "string"},
						"summary":       map[string]any{"type": "string"},
						"risk_level":    map[string]any{"type": "string"},
						"evidence_refs": stringArray(),
					},
				},
			},
			"data_quality_notes":              stringArray(),
			"next_watch_focus":                stringArray(),
			"checked_news_thread_version_ids": stringArray(),
			"impact_review_coverage": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"required":             []string{"holding_ids", "monitor_ids", "alert_ids", "opportunity_ids", "strategy_ids"},
				"properties": map[string]any{
					"holding_ids":     stringArray(),
					"monitor_ids":     stringArray(),
					"alert_ids":       stringArray(),
					"opportunity_ids": stringArray(),
					"strategy_ids":    stringArray(),
				},
			},
		},
	}
	schema := map[string]any{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"taskID", "taskType", "result"},
		"properties": map[string]any{
			"taskID":   map[string]any{"type": "string", "const": taskID},
			"taskType": map[string]any{"type": "string", "const": AgentTaskTypePortfolioSentinel},
			"result": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"required":             []string{"outputType", "resultSummary", "result", "confidence"},
				"properties": map[string]any{
					"outputType":    map[string]any{"type": "string", "const": PortfolioSentinelOutputType},
					"resultSummary": map[string]any{"type": "string"},
					"result":        report,
					"confidence":    map[string]any{"type": "number", "minimum": 0, "maximum": 1},
				},
			},
		},
	}
	raw, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("encode portfolio sentinel output schema: %w", err)
	}
	return raw, nil
}
