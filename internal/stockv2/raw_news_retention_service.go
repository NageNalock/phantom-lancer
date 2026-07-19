package stockv2

import (
	"context"
	"fmt"
	"time"

	"phantom-lancer/internal/safelog"
)

const (
	rawNewsRetentionInterval = time.Hour
	rawNewsRetentionWindow   = 4 * time.Hour
)

type RawNewsRetentionResult struct {
	Cutoff               time.Time `json:"cutoff"`
	ProcessedBeforePrune int       `json:"processedBeforePrune"`
	DeletedCount         int       `json:"deletedCount"`
	Skipped              bool      `json:"skipped"`
}

func (s *Service) runRawNewsRetentionScheduler(ctx context.Context) {
	if s == nil || s.store == nil {
		return
	}
	timer := time.NewTimer(rawNewsRetentionInterval)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			if s.shouldDeferMaintenanceForNewsContextBackfill(ctx) {
				timer.Reset(time.Minute)
				continue
			}
			if !s.tryStartBackgroundHeavyWork() {
				timer.Reset(time.Minute)
				continue
			}
			result, err := s.PruneRawNewsRetention(ctx, time.Now())
			s.finishBackgroundHeavyWork()
			if err != nil {
				if s.log != nil {
					s.log.Warn("prune raw news failed", "error", safelog.Text(err.Error(), 300))
				}
				timer.Reset(rawNewsRetentionInterval)
				continue
			}
			if !result.Skipped && result.DeletedCount > 0 && s.log != nil {
				s.log.Info("pruned raw news", "deleted_count", result.DeletedCount, "processed_before_prune", result.ProcessedBeforePrune, "cutoff", result.Cutoff.Format(time.RFC3339Nano))
			}
			timer.Reset(rawNewsRetentionInterval)
		}
	}
}

func (s *Service) PruneRawNewsRetention(ctx context.Context, now time.Time) (RawNewsRetentionResult, error) {
	if now.IsZero() {
		now = time.Now()
	}
	result := RawNewsRetentionResult{Cutoff: now.Add(-rawNewsRetentionWindow)}
	if s == nil || s.store == nil {
		result.Skipped = true
		return result, nil
	}
	if !s.tryStartNewsPipelineRun() {
		result.Skipped = true
		return result, nil
	}
	defer s.finishNewsPipelineRun()

	for _, source := range []string{NewsSourceJin10, NewsSourceFinancialJuice} {
		for {
			pendingBefore, err := s.store.CountRawNews(ctx, RawNewsListFilter{
				Source: source,
				Status: NewsStatusNew,
				Until:  result.Cutoff,
			})
			if err != nil {
				return result, err
			}
			if pendingBefore == 0 {
				break
			}
			process, err := s.RunNewsProcessingBatch(ctx, source, 200, 200)
			if err != nil {
				return result, err
			}
			result.ProcessedBeforePrune += process.NormalizedCount
			pendingAfter, err := s.store.CountRawNews(ctx, RawNewsListFilter{
				Source: source,
				Status: NewsStatusNew,
				Until:  result.Cutoff,
			})
			if err != nil {
				return result, err
			}
			if pendingAfter >= pendingBefore {
				return result, fmt.Errorf("raw news retention made no progress for source %s: %d items remain", source, pendingAfter)
			}
		}
	}
	deleted, err := s.TruncateRawNewsBefore(ctx, result.Cutoff)
	if err != nil {
		return result, err
	}
	result.DeletedCount = deleted.DeletedCount
	return result, nil
}
