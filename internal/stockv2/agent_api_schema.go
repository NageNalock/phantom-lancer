package stockv2

func agentAPINewsContextSubmitResultSchema(taskID string, pack NewsContextAggregationPack) map[string]any {
	newsIDs := make([]string, 0, len(pack.InputNewsEvents))
	for _, event := range pack.InputNewsEvents {
		newsIDs = append(newsIDs, event.ID)
	}
	threadIDs := make([]string, 0, len(pack.InputThreads))
	for _, thread := range pack.InputThreads {
		threadIDs = append(threadIDs, firstNonEmpty(thread.ID, thread.ThemeID))
	}
	newsIDs = uniqueNonEmptyStrings(newsIDs)
	threadIDs = uniqueNonEmptyStrings(threadIDs)

	stringArray := func(description string) map[string]any {
		return map[string]any{
			"type":        "array",
			"items":       map[string]any{"type": "string"},
			"description": description,
		}
	}
	exactStringArray := func(values []string, description string) map[string]any {
		items := map[string]any{"type": "string"}
		if len(values) > 0 {
			items["enum"] = values
		}
		return map[string]any{
			"type":        "array",
			"items":       items,
			"description": description,
		}
	}
	object := func(properties map[string]any, required ...string) map[string]any {
		return map[string]any{
			"type":                 "object",
			"properties":           properties,
			"required":             required,
			"additionalProperties": false,
		}
	}

	relation := object(map[string]any{
		"targetThemeId":    map[string]any{"type": "string"},
		"targetThemeTitle": map[string]any{"type": "string"},
		"relationType":     map[string]any{"type": "string"},
		"summary":          map[string]any{"type": "string"},
		"strength":         map[string]any{"type": "number", "minimum": 0, "maximum": 1},
	}, "targetThemeId", "targetThemeTitle", "relationType", "summary", "strength")

	decision := object(map[string]any{
		"news_event_id": exactStringSchema(newsIDs, "Exact input news event id."),
		"disposition": map[string]any{
			"type": "string",
			"enum": []string{"create", "update", "support", "contradict", "background", "duplicate", "noise", "defer", "deferred"},
		},
		"thread_id": map[string]any{"type": "string", "description": "Existing stable theme id or one batch-local temporary id for create."},
		"reason":    map[string]any{"type": "string"},
	}, "news_event_id", "disposition", "thread_id", "reason")
	threadChangeID := map[string]any{
		"type":        "string",
		"description": "Empty for create; stable existing theme id for every other action.",
	}
	if len(newsIDs) == 0 {
		threadChangeID["enum"] = append([]string{""}, threadIDs...)
	}

	change := object(map[string]any{
		"action":            map[string]any{"type": "string", "enum": []string{"create", "update", "merge", "split", "restart"}},
		"thread_id":         threadChangeID,
		"source_thread_ids": exactStringArray(threadIDs, "Input stable theme ids consumed by merge or split; empty otherwise."),
		"title":             map[string]any{"type": "string"},
		"core_thesis":       map[string]any{"type": "string"},
		"stage": map[string]any{
			"type": "string",
			"enum": []string{
				NewsThreadStageEmerging, NewsThreadStageSpreading, NewsThreadStageAccelerating,
				NewsThreadStageOverheated, NewsThreadStageDiverging, NewsThreadStageRetreating,
				NewsThreadStageDormant, NewsThreadStageRestarting,
			},
		},
		"latest_change":     map[string]any{"type": "string"},
		"material_change":   map[string]any{"type": "boolean"},
		"confidence":        map[string]any{"type": "number", "minimum": 0, "maximum": 1},
		"industries":        stringArray("Affected industries; empty when none."),
		"symbols":           stringArray("Affected instrument symbols; empty when none."),
		"funds":             stringArray("Affected funds; empty when none."),
		"facts":             stringArray("Confirmed facts."),
		"inferences":        stringArray("Inferences separated from facts."),
		"counter_evidence":  stringArray("Contrary evidence."),
		"open_questions":    stringArray("Unresolved questions."),
		"leaders":           stringArray("Theme leaders."),
		"followers":         stringArray("Theme followers."),
		"laggards":          stringArray("Theme laggards."),
		"next_candidates":   stringArray("Potential relay candidates."),
		"catalysts":         stringArray("Future catalysts."),
		"invalidations":     stringArray("Invalidation conditions."),
		"relations":         map[string]any{"type": "array", "items": relation},
		"evidence_news_ids": exactStringArray(newsIDs, "Exact input news ids used as evidence by this change."),
		"research_status": map[string]any{
			"type": "string",
			"enum": []string{"not_required", "completed", "verified", "failed", "unavailable", "unresolved"},
		},
	},
		"action", "thread_id", "source_thread_ids", "title", "core_thesis", "stage", "latest_change",
		"material_change", "confidence", "industries", "symbols", "funds", "facts", "inferences",
		"counter_evidence", "open_questions", "leaders", "followers", "laggards", "next_candidates",
		"catalysts", "invalidations", "relations", "evidence_news_ids", "research_status",
	)

	audit := object(map[string]any{
		"question":            map[string]any{"type": "string"},
		"status":              map[string]any{"type": "string", "enum": []string{"completed", "verified", "failed", "unavailable"}},
		"sources":             stringArray("Public source URLs; empty for failed or unavailable."),
		"supported":           stringArray("Claims supported by sources."),
		"weakened_or_refuted": stringArray("Claims weakened or refuted by sources."),
		"unresolved":          stringArray("Questions that remain unresolved."),
		"failure_reason":      map[string]any{"type": "string", "description": "Required for failed or unavailable; empty otherwise."},
	}, "question", "status", "sources", "supported", "weakened_or_refuted", "unresolved", "failure_reason")

	report := object(map[string]any{
		"schema_version":       map[string]any{"type": "string", "enum": []string{NewsContextResultSchemaVersion}},
		"run_id":               map[string]any{"type": "string", "enum": []string{pack.RunID}},
		"window_type":          map[string]any{"type": "string", "enum": []string{pack.WindowType}},
		"processed_news_ids":   exactStringArray(newsIDs, "Every input news id exactly once; empty for a thread-only batch."),
		"reviewed_thread_ids":  exactStringArray(threadIDs, "Input theme ids reviewed without an unchanged or changed daily conclusion."),
		"unchanged_thread_ids": exactStringArray(threadIDs, "Input stable theme ids with an explicit unchanged stage conclusion."),
		"news_decisions":       map[string]any{"type": "array", "items": decision},
		"thread_changes":       map[string]any{"type": "array", "items": change},
		"search_audit":         map[string]any{"type": "array", "items": audit},
		"urgent_review":        map[string]any{"type": "boolean"},
	},
		"schema_version", "run_id", "window_type", "processed_news_ids", "reviewed_thread_ids",
		"unchanged_thread_ids", "news_decisions", "thread_changes", "search_audit", "urgent_review",
	)

	result := object(map[string]any{
		"outputType":    map[string]any{"type": "string", "enum": []string{NewsContextOutputType}},
		"resultSummary": map[string]any{"type": "string"},
		"confidence":    map[string]any{"type": "number", "minimum": 0, "maximum": 1},
		"result":        report,
	}, "outputType", "resultSummary", "confidence", "result")

	return object(map[string]any{
		"taskID":   map[string]any{"type": "string", "enum": []string{taskID}},
		"taskType": map[string]any{"type": "string", "enum": []string{AgentTaskTypeNewsEventReview}},
		"result":   result,
	}, "taskID", "taskType", "result")
}

func exactStringSchema(values []string, description string) map[string]any {
	schema := map[string]any{"type": "string", "description": description}
	if len(values) > 0 {
		schema["enum"] = values
	}
	return schema
}
