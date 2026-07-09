package stockv2

import (
	"context"
	"time"
)

func (s *Service) ListAlerts(ctx context.Context, filter AlertListFilter) ([]StockV2Alert, error) {
	return s.store.ListAlerts(ctx, filter)
}

func (s *Service) CountAlerts(ctx context.Context, filter AlertListFilter) (int, error) {
	return s.store.CountAlerts(ctx, filter)
}

func (s *Service) AcknowledgeAlert(ctx context.Context, id string) (StockV2Alert, error) {
	return s.setAlertStatus(ctx, id, AlertStatusAcknowledged)
}

func (s *Service) IgnoreAlert(ctx context.Context, id string) (StockV2Alert, error) {
	return s.setAlertStatus(ctx, id, AlertStatusIgnored)
}

func (s *Service) ResolveAlert(ctx context.Context, id string) (StockV2Alert, error) {
	return s.setAlertStatus(ctx, id, AlertStatusResolved)
}

func (s *Service) setAlertStatus(ctx context.Context, id string, status string) (StockV2Alert, error) {
	if !validAlertStatus(status) {
		return StockV2Alert{}, ErrInvalidAlertStatus
	}
	alert, err := s.store.GetAlert(ctx, id)
	if err != nil {
		return StockV2Alert{}, err
	}
	now := time.Now()
	alert.Status = status
	switch status {
	case AlertStatusAcknowledged, AlertStatusIgnored:
		alert.AcknowledgedAt = now
	case AlertStatusResolved:
		alert.ResolvedAt = now
	}
	return s.store.UpdateAlert(ctx, alert)
}

func validAlertStatus(status string) bool {
	return status == AlertStatusOpen || status == AlertStatusAcknowledged || status == AlertStatusIgnored || status == AlertStatusResolved
}

func validAlertLevel(level string) bool {
	return level == AlertLevelInfo || level == AlertLevelWarning || level == AlertLevelCritical
}
