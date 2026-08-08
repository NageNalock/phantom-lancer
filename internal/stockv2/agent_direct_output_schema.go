package stockv2

import (
	"encoding/json"
	"fmt"
)

func agentDirectOutputSchema(taskID, taskType, outputType string, resultSchema map[string]any) ([]byte, error) {
	schema := map[string]any{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"taskID", "taskType", "result"},
		"properties": map[string]any{
			"taskID":   map[string]any{"type": "string", "const": taskID},
			"taskType": map[string]any{"type": "string", "const": taskType},
			"result": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"required":             []string{"outputType", "resultSummary", "result", "confidence"},
				"properties": map[string]any{
					"outputType":    map[string]any{"type": "string", "const": outputType},
					"resultSummary": map[string]any{"type": "string"},
					"result":        resultSchema,
					"confidence":    map[string]any{"type": "number", "minimum": 0, "maximum": 1},
				},
			},
		},
	}
	// ponytail: DeepSeek strict schemas require every object property in
	// `required`. Preserve optional domain fields as nullable instead of keeping
	// a second provider-specific result model.
	requireAllSchemaProperties(schema)
	raw, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("encode %s output schema: %w", taskType, err)
	}
	return raw, nil
}

func stockProfileDirectOutputSchema(taskID string) ([]byte, error) {
	stringArray := map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
	result := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required": []string{
			"summaryZh", "summaryEn", "aliasesZh", "aliasesEn", "keywordsZh", "keywordsEn",
			"businessLinesZh", "businessLinesEn", "riskTagsZh", "riskTagsEn", "sourceNotes",
		},
		"properties": map[string]any{
			"summaryZh":       map[string]any{"type": "string"},
			"summaryEn":       map[string]any{"type": "string"},
			"aliasesZh":       stringArray,
			"aliasesEn":       stringArray,
			"keywordsZh":      stringArray,
			"keywordsEn":      stringArray,
			"businessLinesZh": stringArray,
			"businessLinesEn": stringArray,
			"riskTagsZh":      stringArray,
			"riskTagsEn":      stringArray,
			"sourceNotes":     stringArray,
		},
	}
	return agentDirectOutputSchema(taskID, AgentTaskTypeStockProfileSummary, AgentTaskTypeStockProfileSummary, result)
}

func newsContextDirectOutputSchema(taskID, runID, windowType string) ([]byte, error) {
	stringArray := func() map[string]any {
		return map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
	}
	relation := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"relationType"},
		"properties": map[string]any{
			"targetThemeId":    map[string]any{"type": "string"},
			"targetThemeTitle": map[string]any{"type": "string"},
			"relationType":     map[string]any{"type": "string"},
			"summary":          map[string]any{"type": "string"},
			"strength":         map[string]any{"type": "number", "minimum": 0, "maximum": 1},
		},
	}
	decision := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"news_event_id", "disposition"},
		"properties": map[string]any{
			"news_event_id": map[string]any{"type": "string"},
			"disposition": map[string]any{"type": "string", "enum": []string{
				"create", "update", "support", "contradict", "background", "duplicate", "noise", NewsEventContextDeferred,
			}},
			"thread_id": map[string]any{"type": "string"},
			"reason":    map[string]any{"type": "string"},
		},
	}
	change := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required": []string{
			"action", "title", "core_thesis", "stage", "material_change", "confidence",
			"industries", "symbols", "funds", "facts", "inferences", "counter_evidence",
			"open_questions", "leaders", "followers", "laggards", "next_candidates",
			"catalysts", "invalidations", "relations", "evidence_news_ids", "research_status",
		},
		"properties": map[string]any{
			"action":            map[string]any{"type": "string", "enum": []string{"create", "update", "merge", "split", "restart"}},
			"thread_id":         map[string]any{"type": "string"},
			"source_thread_ids": stringArray(),
			"title":             map[string]any{"type": "string"},
			"core_thesis":       map[string]any{"type": "string"},
			"stage": map[string]any{"type": "string", "enum": []string{
				NewsThreadStageEmerging, NewsThreadStageSpreading, NewsThreadStageAccelerating,
				NewsThreadStageOverheated, NewsThreadStageDiverging, NewsThreadStageRetreating,
				NewsThreadStageDormant, NewsThreadStageRestarting,
			}},
			"latest_change":     map[string]any{"type": "string"},
			"material_change":   map[string]any{"type": "boolean"},
			"confidence":        map[string]any{"type": "number", "minimum": 0, "maximum": 1},
			"industries":        stringArray(),
			"symbols":           stringArray(),
			"funds":             stringArray(),
			"facts":             stringArray(),
			"inferences":        stringArray(),
			"counter_evidence":  stringArray(),
			"open_questions":    stringArray(),
			"leaders":           stringArray(),
			"followers":         stringArray(),
			"laggards":          stringArray(),
			"next_candidates":   stringArray(),
			"catalysts":         stringArray(),
			"invalidations":     stringArray(),
			"relations":         map[string]any{"type": "array", "items": relation},
			"evidence_news_ids": stringArray(),
			"research_status": map[string]any{"type": "string", "enum": []string{
				NewsContextResearchNotRequired, NewsContextResearchCompleted, "verified",
				NewsContextResearchUnresolved, NewsContextResearchFailed, NewsContextResearchUnavailable,
			}},
		},
	}
	searchAudit := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"question", "status", "sources", "supported", "weakened_or_refuted", "unresolved"},
		"properties": map[string]any{
			"question": map[string]any{"type": "string"},
			"status": map[string]any{"type": "string", "enum": []string{
				NewsContextResearchCompleted, "verified", NewsContextResearchFailed, NewsContextResearchUnavailable,
			}},
			"sources":             stringArray(),
			"supported":           stringArray(),
			"weakened_or_refuted": stringArray(),
			"unresolved":          stringArray(),
			"failure_reason":      map[string]any{"type": "string"},
		},
	}
	runIDSchema := map[string]any{"type": "string"}
	if runID != "" {
		runIDSchema["const"] = runID
	}
	windowSchema := map[string]any{"type": "string", "enum": []string{NewsContextWindowHourly, NewsContextWindowFourHour, NewsContextWindowDaily}}
	if validNewsContextWindowType(windowType) {
		windowSchema = map[string]any{"type": "string", "const": windowType}
	}
	result := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required": []string{
			"schema_version", "run_id", "window_type", "processed_news_ids", "reviewed_thread_ids",
			"unchanged_thread_ids", "news_decisions", "thread_changes", "search_audit", "urgent_review",
		},
		"properties": map[string]any{
			"schema_version":       map[string]any{"type": "string", "const": NewsContextResultSchemaVersion},
			"run_id":               runIDSchema,
			"window_type":          windowSchema,
			"processed_news_ids":   stringArray(),
			"reviewed_thread_ids":  stringArray(),
			"unchanged_thread_ids": stringArray(),
			"news_decisions":       map[string]any{"type": "array", "items": decision},
			"thread_changes":       map[string]any{"type": "array", "items": change},
			"search_audit":         map[string]any{"type": "array", "items": searchAudit},
			"urgent_review":        map[string]any{"type": "boolean"},
		},
	}
	return agentDirectOutputSchema(taskID, AgentTaskTypeNewsEventReview, NewsContextOutputType, result)
}
