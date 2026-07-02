package stockv2

import (
	"context"
	"time"

	"phantom-lancer/internal/safelog"
)

const newsLinkCandidateRetentionInterval = time.Hour

func (s *Service) runNewsLinkCandidateRetentionScheduler(ctx context.Context) {
	if s == nil || s.store == nil {
		return
	}
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			result, err := s.store.PruneNewsLinkCandidates(ctx, time.Now())
			if err != nil {
				if s.log != nil {
					s.log.Warn("prune news link candidates failed", "error", safelog.Text(err.Error(), 300))
				}
				timer.Reset(newsLinkCandidateRetentionInterval)
				continue
			}
			if result.DeletedTotal > 0 && s.log != nil {
				s.log.Info("pruned news link candidates", "deleted_total", result.DeletedTotal, "deleted_skipped_failed", result.DeletedSkippedFailed, "deleted_low_confidence", result.DeletedLowConfidence)
			}
			timer.Reset(newsLinkCandidateRetentionInterval)
		}
	}
}
