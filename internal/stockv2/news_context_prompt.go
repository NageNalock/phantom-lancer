package stockv2

import (
	"time"

	"phantom-lancer/internal/safelog"
)

const (
	newsContextRunPhaseAggregating = "aggregating"
	newsContextRunPhaseCheckpoint  = "checkpointing"
	newsContextRunPhaseMaterialize = "materializing"
)

func compactNewsThreadForPrompt(thread NewsThread) NewsContextPromptThread {
	// ponytail: fixed clipping is an internal prompt-safety boundary, not a
	// business limit. Persisted input items keep complete coverage; if one
	// snapshot outgrows this schema, tighten the schema instead of adding a
	// second owner-tuned batch configuration.
	thread.Title = safelog.Text(thread.Title, 160)
	thread.CoreThesis = safelog.Text(thread.CoreThesis, 600)
	thread.Summary = safelog.Text(thread.Summary, 600)
	if thread.Summary == thread.CoreThesis {
		thread.Summary = ""
	}
	thread.LatestChange = safelog.Text(thread.LatestChange, 500)
	thread.IndexError = safelog.Text(thread.IndexError, 160)
	thread.DataConfirmation = safelog.Text(thread.DataConfirmation, 160)
	thread.Industries = compactNewsContextPromptStrings(thread.Industries, 6, 48)
	thread.Symbols = compactNewsContextPromptStrings(thread.Symbols, 6, 48)
	thread.Funds = compactNewsContextPromptStrings(thread.Funds, 6, 48)
	thread.Leaders = compactNewsContextPromptStrings(thread.Leaders, 6, 48)
	thread.Followers = compactNewsContextPromptStrings(thread.Followers, 6, 48)
	thread.Laggards = compactNewsContextPromptStrings(thread.Laggards, 6, 48)
	thread.NextCandidates = compactNewsContextPromptStrings(thread.NextCandidates, 6, 48)
	thread.Facts = compactNewsContextPromptStrings(thread.Facts, 2, 160)
	thread.Inferences = compactNewsContextPromptStrings(thread.Inferences, 2, 160)
	thread.CounterEvidence = compactNewsContextPromptStrings(thread.CounterEvidence, 2, 160)
	thread.OpenQuestions = compactNewsContextPromptStrings(thread.OpenQuestions, 2, 160)
	thread.Catalysts = compactNewsContextPromptStrings(thread.Catalysts, 2, 160)
	thread.Invalidations = compactNewsContextPromptStrings(thread.Invalidations, 2, 160)
	thread.ConfirmationSignals = compactNewsContextPromptStrings(thread.ConfirmationSignals, 2, 120)
	thread.InvalidationSignals = compactNewsContextPromptStrings(thread.InvalidationSignals, 2, 120)
	if len(thread.Relations) > 3 {
		thread.Relations = thread.Relations[:3]
	}
	thread.Relations = append([]NewsThreadRelation(nil), thread.Relations...)
	for i := range thread.Relations {
		thread.Relations[i].ThreadID = safelog.Text(thread.Relations[i].ThreadID, 100)
		thread.Relations[i].Title = safelog.Text(thread.Relations[i].Title, 60)
		thread.Relations[i].Type = safelog.Text(thread.Relations[i].Type, 40)
		thread.Relations[i].Reason = safelog.Text(thread.Relations[i].Reason, 120)
	}
	effectiveAt := ""
	if !thread.LastChangedAt.IsZero() {
		effectiveAt = thread.LastChangedAt.Format(time.RFC3339)
	}
	return NewsContextPromptThread{
		ID: thread.ID, ThemeID: thread.ThemeID, Title: thread.Title, Summary: thread.Summary,
		CoreThesis: thread.CoreThesis, Stage: thread.Stage,
		LatestChange: thread.LatestChange, Confidence: thread.Confidence,
		Status: thread.Status, Industries: thread.Industries, Symbols: thread.Symbols,
		Funds: thread.Funds, Facts: thread.Facts, Inferences: thread.Inferences,
		CounterEvidence: thread.CounterEvidence, OpenQuestions: thread.OpenQuestions,
		Leaders: thread.Leaders, Followers: thread.Followers, Laggards: thread.Laggards,
		NextCandidates: thread.NextCandidates, Catalysts: thread.Catalysts,
		Invalidations: thread.Invalidations, Relations: thread.Relations,
		CurrentVersion: thread.CurrentVersion, CurrentVersionID: thread.CurrentVersionID,
		EffectiveAt:         effectiveAt,
		DataConfirmation:    thread.DataConfirmation,
		ConfirmationSignals: thread.ConfirmationSignals,
		InvalidationSignals: thread.InvalidationSignals,
	}
}

func compactNewsContextPromptStrings(values []string, limit, runeLimit int) []string {
	if len(values) > limit {
		values = values[:limit]
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value = safelog.Text(value, runeLimit); value != "" {
			out = append(out, value)
		}
	}
	return out
}
