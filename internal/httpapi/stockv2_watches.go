package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"phantom-lancer/internal/stockv2"
)

func (s *Server) handleStockV2ListWatches(w http.ResponseWriter, r *http.Request) {
	filter, err := stockV2WatchFilterFromRequest(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	items, err := s.stockV2.ListWatches(r.Context(), filter)
	if err != nil {
		http.Error(w, err.Error(), stockV2WatchHTTPStatus(err))
		return
	}
	total, err := s.stockV2.CountWatches(r.Context(), filter)
	if err != nil {
		http.Error(w, err.Error(), stockV2WatchHTTPStatus(err))
		return
	}
	s.writeJSON(w, map[string]any{
		"items":  items,
		"total":  total,
		"limit":  filter.Limit,
		"offset": filter.Offset,
	})
}

func (s *Server) handleStockV2CreateWatch(w http.ResponseWriter, r *http.Request) {
	var req stockv2.RequestCreateWatch
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	result, err := s.stockV2.CreateWatch(r.Context(), req)
	if err != nil {
		http.Error(w, err.Error(), stockV2WatchHTTPStatus(err))
		return
	}
	s.writeJSON(w, result)
}

func (s *Server) handleStockV2GetWatch(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "watch ID is required", http.StatusBadRequest)
		return
	}
	result, err := s.stockV2.GetWatch(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), stockV2WatchHTTPStatus(err))
		return
	}
	s.writeJSON(w, result)
}

func (s *Server) handleStockV2UpdateWatch(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "watch ID is required", http.StatusBadRequest)
		return
	}
	var req stockv2.RequestUpdateWatch
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	result, err := s.stockV2.UpdateWatch(r.Context(), id, req)
	if err != nil {
		http.Error(w, err.Error(), stockV2WatchHTTPStatus(err))
		return
	}
	s.writeJSON(w, result)
}

func (s *Server) handleStockV2ActivateWatch(w http.ResponseWriter, r *http.Request) {
	s.handleStockV2WatchStatusAction(w, r, s.stockV2.ActivateWatch)
}

func (s *Server) handleStockV2PauseWatch(w http.ResponseWriter, r *http.Request) {
	s.handleStockV2WatchStatusAction(w, r, s.stockV2.PauseWatch)
}

func (s *Server) handleStockV2ArchiveWatch(w http.ResponseWriter, r *http.Request) {
	s.handleStockV2WatchStatusAction(w, r, s.stockV2.ArchiveWatch)
}

func (s *Server) handleStockV2RunWatch(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "watch ID is required", http.StatusBadRequest)
		return
	}
	result, err := s.stockV2.RunWatch(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), stockV2WatchHTTPStatus(err))
		return
	}
	s.writeJSON(w, result)
}

func (s *Server) handleStockV2CreateStrategyWatch(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "strategy ID is required", http.StatusBadRequest)
		return
	}
	result, err := s.stockV2.CreateStrategyWatch(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), stockV2WatchHTTPStatus(err))
		return
	}
	s.writeJSON(w, result)
}

func (s *Server) handleStockV2CreatePortfolioMonitorWatch(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "portfolio ID is required", http.StatusBadRequest)
		return
	}
	result, err := s.stockV2.CreatePortfolioMonitorWatch(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), stockV2WatchHTTPStatus(err))
		return
	}
	s.writeJSON(w, result)
}

func (s *Server) handleStockV2ListAlerts(w http.ResponseWriter, r *http.Request) {
	filter, err := stockV2AlertFilterFromRequest(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	items, err := s.stockV2.ListAlerts(r.Context(), filter)
	if err != nil {
		http.Error(w, err.Error(), stockV2WatchHTTPStatus(err))
		return
	}
	total, err := s.stockV2.CountAlerts(r.Context(), filter)
	if err != nil {
		http.Error(w, err.Error(), stockV2WatchHTTPStatus(err))
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

func (s *Server) handleStockV2WatchStatusAction(w http.ResponseWriter, r *http.Request, action func(context.Context, string) (stockv2.StockV2Watch, error)) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "watch ID is required", http.StatusBadRequest)
		return
	}
	result, err := action(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), stockV2WatchHTTPStatus(err))
		return
	}
	s.writeJSON(w, result)
}

func (s *Server) handleStockV2AlertStatusAction(w http.ResponseWriter, r *http.Request, action func(context.Context, string) (stockv2.StockV2Alert, error)) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "alert ID is required", http.StatusBadRequest)
		return
	}
	result, err := action(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), stockV2WatchHTTPStatus(err))
		return
	}
	s.writeJSON(w, result)
}

func stockV2WatchFilterFromRequest(r *http.Request) (stockv2.WatchListFilter, error) {
	query := r.URL.Query()
	limit, err := stockV2WatchPositiveInt(query.Get("limit"), 50)
	if err != nil || limit > 200 {
		return stockv2.WatchListFilter{}, errors.New("invalid limit")
	}
	offset, err := stockV2WatchNonNegativeInt(query.Get("offset"), 0)
	if err != nil {
		return stockv2.WatchListFilter{}, errors.New("invalid offset")
	}
	status := query.Get("status")
	if status != "" && !stockV2HTTPValidWatchStatus(status) {
		return stockv2.WatchListFilter{}, errors.New("invalid watch status")
	}
	return stockv2.WatchListFilter{
		Status:      status,
		PortfolioID: query.Get("portfolioId"),
		StrategyID:  query.Get("strategyId"),
		Symbol:      query.Get("symbol"),
		Limit:       limit,
		Offset:      offset,
	}, nil
}

func stockV2AlertFilterFromRequest(r *http.Request) (stockv2.AlertListFilter, error) {
	query := r.URL.Query()
	limit, err := stockV2WatchPositiveInt(query.Get("limit"), 50)
	if err != nil || limit > 200 {
		return stockv2.AlertListFilter{}, errors.New("invalid limit")
	}
	offset, err := stockV2WatchNonNegativeInt(query.Get("offset"), 0)
	if err != nil {
		return stockv2.AlertListFilter{}, errors.New("invalid offset")
	}
	status := query.Get("status")
	if status != "" && !stockV2HTTPValidAlertStatus(status) {
		return stockv2.AlertListFilter{}, errors.New("invalid alert status")
	}
	return stockv2.AlertListFilter{
		Status:  status,
		WatchID: query.Get("watchId"),
		Limit:   limit,
		Offset:  offset,
	}, nil
}

func stockV2WatchHTTPStatus(err error) int {
	switch {
	case errors.Is(err, stockv2.ErrWatchNotFound),
		errors.Is(err, stockv2.ErrAlertNotFound),
		errors.Is(err, stockv2.ErrPortfolioNotFound),
		errors.Is(err, stockv2.ErrStrategyNotFound),
		errors.Is(err, stockv2.ErrStrategyVersionNotFound):
		return http.StatusNotFound
	case errors.Is(err, stockv2.ErrWatchArchived),
		errors.Is(err, stockv2.ErrWatchNotActive):
		return http.StatusConflict
	case errors.Is(err, stockv2.ErrInvalidWatchName),
		errors.Is(err, stockv2.ErrInvalidWatchTarget),
		errors.Is(err, stockv2.ErrInvalidWatchStatus),
		errors.Is(err, stockv2.ErrInvalidWatchSource),
		errors.Is(err, stockv2.ErrInvalidWatchPolicy),
		errors.Is(err, stockv2.ErrInvalidWatchSchedule),
		errors.Is(err, stockv2.ErrInvalidWatchCooldown),
		errors.Is(err, stockv2.ErrInvalidAlertTitle),
		errors.Is(err, stockv2.ErrInvalidAlertStatus),
		errors.Is(err, stockv2.ErrInvalidAlertLevel),
		errors.Is(err, stockv2.ErrInvalidStrategySymbol):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

func stockV2HTTPValidWatchStatus(status string) bool {
	return status == stockv2.WatchStatusActive ||
		status == stockv2.WatchStatusPaused ||
		status == stockv2.WatchStatusArchived
}

func stockV2HTTPValidAlertStatus(status string) bool {
	return status == stockv2.AlertStatusOpen ||
		status == stockv2.AlertStatusAcknowledged ||
		status == stockv2.AlertStatusIgnored ||
		status == stockv2.AlertStatusResolved
}

func stockV2WatchPositiveInt(raw string, fallback int) (int, error) {
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return 0, errors.New("invalid positive integer")
	}
	return value, nil
}

func stockV2WatchNonNegativeInt(raw string, fallback int) (int, error) {
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return 0, errors.New("invalid non-negative integer")
	}
	return value, nil
}
