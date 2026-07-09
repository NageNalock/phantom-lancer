package httpapi

import (
	"context"
	"errors"
	"net/http"

	"phantom-lancer/internal/stockv2"
)

func (s *Server) handleStockV2ListAlerts(w http.ResponseWriter, r *http.Request) {
	filter, err := stockV2AlertFilterFromRequest(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	items, err := s.stockV2.ListAlerts(r.Context(), filter)
	if err != nil {
		http.Error(w, err.Error(), stockV2AlertHTTPStatus(err))
		return
	}
	total, err := s.stockV2.CountAlerts(r.Context(), filter)
	if err != nil {
		http.Error(w, err.Error(), stockV2AlertHTTPStatus(err))
		return
	}
	s.writeJSON(w, map[string]any{
		"items":  items,
		"total":  total,
		"limit":  filter.Limit,
		"offset": filter.Offset,
	})
}

func (s *Server) handleStockV2AckAlert(w http.ResponseWriter, r *http.Request) {
	s.handleStockV2AlertStatusAction(w, r, s.stockV2.AcknowledgeAlert)
}

func (s *Server) handleStockV2IgnoreAlert(w http.ResponseWriter, r *http.Request) {
	s.handleStockV2AlertStatusAction(w, r, s.stockV2.IgnoreAlert)
}

func (s *Server) handleStockV2ResolveAlert(w http.ResponseWriter, r *http.Request) {
	s.handleStockV2AlertStatusAction(w, r, s.stockV2.ResolveAlert)
}

func (s *Server) handleStockV2AlertStatusAction(w http.ResponseWriter, r *http.Request, action func(context.Context, string) (stockv2.StockV2Alert, error)) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "alert ID is required", http.StatusBadRequest)
		return
	}
	result, err := action(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), stockV2AlertHTTPStatus(err))
		return
	}
	s.writeJSON(w, result)
}

func stockV2AlertFilterFromRequest(r *http.Request) (stockv2.AlertListFilter, error) {
	query := r.URL.Query()
	limit, err := stockV2PositiveInt(query.Get("limit"), 50)
	if err != nil || limit > 200 {
		return stockv2.AlertListFilter{}, errors.New("invalid limit")
	}
	offset, err := stockV2NonNegativeInt(query.Get("offset"), 0)
	if err != nil {
		return stockv2.AlertListFilter{}, errors.New("invalid offset")
	}
	status := query.Get("status")
	if status != "" && !stockV2HTTPValidAlertStatus(status) {
		return stockv2.AlertListFilter{}, errors.New("invalid alert status")
	}
	return stockv2.AlertListFilter{
		Status:       status,
		MonitorHitID: query.Get("monitorHitId"),
		ReviewID:     query.Get("reviewId"),
		TaskType:     query.Get("taskType"),
		Symbol:       query.Get("symbol"),
		PortfolioID:  query.Get("portfolioId"),
		StrategyID:   query.Get("strategyId"),
		Limit:        limit,
		Offset:       offset,
	}, nil
}

func stockV2AlertHTTPStatus(err error) int {
	switch {
	case errors.Is(err, stockv2.ErrAlertNotFound):
		return http.StatusNotFound
	case errors.Is(err, stockv2.ErrInvalidAlertTitle),
		errors.Is(err, stockv2.ErrInvalidAlertStatus),
		errors.Is(err, stockv2.ErrInvalidAlertLevel):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

func stockV2HTTPValidAlertStatus(status string) bool {
	return stockV2HTTPValueIn(status, stockv2.AlertStatusOpen, stockv2.AlertStatusAcknowledged, stockv2.AlertStatusIgnored, stockv2.AlertStatusResolved)
}
