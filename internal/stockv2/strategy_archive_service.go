package stockv2

import (
	"context"
	"time"

	"phantom-lancer/internal/safelog"
)

const (
	// ponytail: three days is the owner-defined product rule, not an operational
	// tuning knob. Keep it fixed until the product needs a user-facing setting.
	strategyAutoArchiveAfter    = 72 * time.Hour
	strategyAutoArchiveInterval = time.Hour
)

func (s *Service) runStrategyArchiveScheduler(ctx context.Context) {
	if s == nil || s.store == nil {
		return
	}
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-timer.C:
			count, err := s.archiveInactiveStrategies(ctx, now)
			if err != nil {
				if s.log != nil {
					s.log.Warn("archive inactive strategies failed", "error", safelog.Text(err.Error(), 300))
				}
			} else if count > 0 && s.log != nil {
				s.log.Info("archived inactive strategies", "archived_count", count)
			}
			timer.Reset(strategyAutoArchiveInterval)
		}
	}
}

func (s *Service) archiveInactiveStrategies(ctx context.Context, now time.Time) (int64, error) {
	if s == nil || s.store == nil {
		return 0, nil
	}
	if now.IsZero() {
		now = time.Now()
	}
	return s.store.ArchiveInactiveStrategies(ctx, now.Add(-strategyAutoArchiveAfter), now)
}
