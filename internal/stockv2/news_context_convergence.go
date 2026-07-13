package stockv2

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"phantom-lancer/internal/safelog"
)

const (
	newsContextRunPhaseAggregating = "aggregating"
	newsContextRunPhaseConverging  = "converging"
)

func (s *Service) beginDailyNewsContextConvergence(ctx context.Context, run NewsContextRun) (bool, error) {
	if run.WindowType != NewsContextWindowDaily || run.Phase != newsContextRunPhaseAggregating {
		return false, nil
	}
	versions, err := s.latestNewsContextRunVersions(ctx, run.ID)
	if err != nil {
		return false, err
	}
	return s.store.BeginDailyNewsContextConvergence(ctx, run.ID, versions)
}

func (s *Service) yieldNewsContextBackfillAfterConvergenceStart(ctx context.Context, runID string) error {
	run, err := s.store.GetNewsContextRun(ctx, runID)
	if err != nil {
		return err
	}
	run.Status = NewsContextRunStatusPending
	run.CurrentAgentRunID = ""
	run.ErrorMessage = ""
	if _, err := s.store.UpdateNewsContextRun(ctx, run); err != nil {
		return err
	}
	if backfill, found, err := s.store.NewsContextBackfillForRun(ctx, runID); err != nil {
		return err
	} else if found {
		_, err = s.refreshAndSaveNewsContextBackfill(ctx, backfill)
		return err
	}
	return nil
}

func (s *Service) latestNewsContextRunVersions(ctx context.Context, runID string) ([]NewsThreadVersion, error) {
	latest := make(map[string]NewsThreadVersion)
	for offset := 0; ; offset += newsContextSeedPageSize {
		page, err := s.store.ListNewsThreadVersions(ctx, NewsThreadVersionListFilter{
			RunID: runID, Limit: newsContextSeedPageSize, Offset: offset,
		})
		if err != nil {
			return nil, err
		}
		for _, version := range page {
			current, found := latest[version.ThreadID]
			if !found || version.EffectiveAt.After(current.EffectiveAt) ||
				(version.EffectiveAt.Equal(current.EffectiveAt) && version.VersionNo > current.VersionNo) ||
				(version.EffectiveAt.Equal(current.EffectiveAt) && version.VersionNo == current.VersionNo && version.ID > current.ID) {
				latest[version.ThreadID] = version
			}
		}
		if len(page) < newsContextSeedPageSize {
			break
		}
	}
	versions := make([]NewsThreadVersion, 0, len(latest))
	for _, version := range latest {
		versions = append(versions, version)
	}
	sort.Slice(versions, func(i, j int) bool { return versions[i].ThreadID < versions[j].ThreadID })
	return versions, nil
}

// BeginDailyNewsContextConvergence atomically replaces first-pass theme inputs
// with one final first-pass version per stable theme. Completed news remains the
// durable coverage manifest for the whole daily run.
func (s *Store) BeginDailyNewsContextConvergence(ctx context.Context, runID string, versions []NewsThreadVersion) (bool, error) {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return false, ErrInvalidNewsContextInput
	}
	seen := make(map[string]struct{}, len(versions))
	for _, version := range versions {
		if strings.TrimSpace(version.ID) == "" || strings.TrimSpace(version.ThreadID) == "" {
			return false, ErrInvalidNewsContextInput
		}
		if _, exists := seen[version.ThreadID]; exists {
			return false, fmt.Errorf("%w: duplicate daily convergence theme", ErrInvalidNewsContextInput)
		}
		seen[version.ThreadID] = struct{}{}
	}
	transitioned := false
	err := s.runTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var windowType, status, phase string
		if err := tx.QueryRowContext(ctx, `SELECT window_type,status,COALESCE(phase,'')
			FROM stockv2_news_context_runs WHERE id=?`, runID).Scan(&windowType, &status, &phase); err != nil {
			return wrapError(err, "load daily run for convergence")
		}
		if windowType != NewsContextWindowDaily {
			return ErrInvalidNewsContextInput
		}
		if phase == newsContextRunPhaseConverging {
			return nil
		}
		if phase != newsContextRunPhaseAggregating || status != NewsContextRunStatusRunning {
			return ErrInvalidNewsContextInput
		}
		var unfinished int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM stockv2_news_context_run_items
			WHERE run_id=? AND status<>?`, runID, NewsContextRunItemCompleted).Scan(&unfinished); err != nil {
			return wrapError(err, "check first-pass daily items")
		}
		if unfinished != 0 {
			return fmt.Errorf("%w: daily first pass is incomplete", ErrInvalidNewsContextInput)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM stockv2_news_context_run_items
			WHERE run_id=? AND object_type=?`, runID, NewsContextRunItemThread); err != nil {
			return wrapError(err, "replace daily first-pass theme items")
		}
		now := time.Now()
		for _, version := range versions {
			if _, err := tx.ExecContext(ctx, `INSERT INTO stockv2_news_context_run_items
				(id,run_id,object_type,object_id,status,thread_id,version_id,source_at,created_at,updated_at)
				VALUES (?,?,?,?,?,?,?,?,?,?)`, generateID(), runID, NewsContextRunItemThread,
				version.ID, NewsContextRunItemPending, version.ThreadID, version.ID,
				nullableTime(version.EffectiveAt), now, now); err != nil {
				return wrapError(err, "insert daily convergence theme item")
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE stockv2_news_context_runs SET
			phase=?,current_agent_run_id=NULL,
			input_count=(SELECT COUNT(*) FROM stockv2_news_context_run_items WHERE run_id=?),
			processed_count=(SELECT COUNT(*) FROM stockv2_news_context_run_items WHERE run_id=? AND status=?),
			pending_count=(SELECT COUNT(*) FROM stockv2_news_context_run_items WHERE run_id=? AND status=?),
			updated_at=? WHERE id=?`, newsContextRunPhaseConverging, runID, runID,
			NewsContextRunItemCompleted, runID, NewsContextRunItemPending, now, runID); err != nil {
			return wrapError(err, "begin daily convergence")
		}
		transitioned = true
		return nil
	})
	return transitioned, err
}

func compactNewsThreadForPrompt(thread NewsThread) NewsThread {
	// ponytail: fixed clipping is an internal prompt-safety boundary, not a
	// business limit. The persisted slicer keeps every version and splits an
	// oversized stable-theme history across calls; a single clipped snapshot
	// exceeding the limit must move to a smaller snapshot schema, not a UI limit.
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
	return thread
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
