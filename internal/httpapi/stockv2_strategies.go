package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"phantom-lancer/internal/stockv2"
)

func (s *Server) handleStockV2ListStrategies(w http.ResponseWriter, r *http.Request) {
	filter, err := stockV2StrategyFilterFromRequest(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	items, err := s.stockV2.ListStrategies(r.Context(), filter)
	if err != nil {
		http.Error(w, err.Error(), stockV2StrategyHTTPStatus(err))
		return
	}
	total, err := s.stockV2.CountStrategies(r.Context(), filter)
	if err != nil {
		http.Error(w, err.Error(), stockV2StrategyHTTPStatus(err))
		return
	}
	s.writeJSON(w, map[string]any{
		"items":  items,
		"total":  total,
		"limit":  filter.Limit,
		"offset": filter.Offset,
	})
}

func (s *Server) handleStockV2CreateStrategy(w http.ResponseWriter, r *http.Request) {
	var req stockv2.RequestCreateStrategy
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	result, err := s.stockV2.CreateStrategy(r.Context(), req)
	if err != nil {
		http.Error(w, err.Error(), stockV2StrategyHTTPStatus(err))
		return
	}
	s.writeJSON(w, result)
}

func (s *Server) handleStockV2GetStrategy(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "strategy ID is required", http.StatusBadRequest)
		return
	}
	result, err := s.stockV2.GetStrategy(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), stockV2StrategyHTTPStatus(err))
		return
	}
	s.writeJSON(w, result)
}

func (s *Server) handleStockV2UpdateStrategy(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "strategy ID is required", http.StatusBadRequest)
		return
	}
	var req stockv2.RequestUpdateStrategy
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	result, err := s.stockV2.UpdateStrategy(r.Context(), id, req)
	if err != nil {
		http.Error(w, err.Error(), stockV2StrategyHTTPStatus(err))
		return
	}
	s.writeJSON(w, result)
}

func (s *Server) handleStockV2ActivateStrategy(w http.ResponseWriter, r *http.Request) {
	s.handleStockV2StrategyStatusAction(w, r, s.stockV2.ActivateStrategy)
}

func (s *Server) handleStockV2PauseStrategy(w http.ResponseWriter, r *http.Request) {
	s.handleStockV2StrategyStatusAction(w, r, s.stockV2.PauseStrategy)
}

func (s *Server) handleStockV2ArchiveStrategy(w http.ResponseWriter, r *http.Request) {
	s.handleStockV2StrategyStatusAction(w, r, s.stockV2.ArchiveStrategy)
}

func (s *Server) handleStockV2ListStrategyVersions(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "strategy ID is required", http.StatusBadRequest)
		return
	}
	versions, err := s.stockV2.ListStrategyVersions(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), stockV2StrategyHTTPStatus(err))
		return
	}
	s.writeJSON(w, map[string]any{"items": versions})
}

func (s *Server) handleStockV2CreatePortfolioMonitorStrategy(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "portfolio ID is required", http.StatusBadRequest)
		return
	}
	var req stockv2.RequestCreatePortfolioMonitorStrategy
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	result, err := s.stockV2.CreatePortfolioMonitorStrategy(r.Context(), id, req)
	if err != nil {
		http.Error(w, err.Error(), stockV2StrategyHTTPStatus(err))
		return
	}
	s.writeJSON(w, result)
}

func (s *Server) handleStockV2StrategyStatusAction(w http.ResponseWriter, r *http.Request, action func(context.Context, string) (stockv2.StrategyWithVersion, error)) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "strategy ID is required", http.StatusBadRequest)
		return
	}
	result, err := action(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), stockV2StrategyHTTPStatus(err))
		return
	}
	s.writeJSON(w, result)
}

func stockV2StrategyFilterFromRequest(r *http.Request) (stockv2.StrategyListFilter, error) {
	query := r.URL.Query()
	limit, err := stockV2StrategyPositiveInt(query.Get("limit"), 50)
	if err != nil || limit > 200 {
		return stockv2.StrategyListFilter{}, errors.New("invalid limit")
	}
	offset, err := stockV2StrategyNonNegativeInt(query.Get("offset"), 0)
	if err != nil {
		return stockv2.StrategyListFilter{}, errors.New("invalid offset")
	}
	return stockv2.StrategyListFilter{
		Status:      query.Get("status"),
		Kind:        query.Get("kind"),
		Scope:       query.Get("scope"),
		PortfolioID: query.Get("portfolioId"),
		Symbol:      query.Get("symbol"),
		Keyword:     firstNonEmptyStrategyQuery(query.Get("q"), query.Get("keyword")),
		Limit:       limit,
		Offset:      offset,
	}, nil
}

func firstNonEmptyStrategyQuery(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func stockV2StrategyHTTPStatus(err error) int {
	switch {
	case errors.Is(err, stockv2.ErrStrategyNotFound),
		errors.Is(err, stockv2.ErrStrategyVersionNotFound),
		errors.Is(err, stockv2.ErrPortfolioNotFound):
		return http.StatusNotFound
	case errors.Is(err, stockv2.ErrStrategyArchived):
		return http.StatusConflict
	case errors.Is(err, stockv2.ErrInvalidStrategyName),
		errors.Is(err, stockv2.ErrInvalidStrategySymbol),
		errors.Is(err, stockv2.ErrInvalidStrategyKind),
		errors.Is(err, stockv2.ErrInvalidStrategyScope),
		errors.Is(err, stockv2.ErrInvalidStrategySource),
		errors.Is(err, stockv2.ErrInvalidStrategyStatus),
		errors.Is(err, stockv2.ErrInvalidStrategyDirection):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

func stockV2StrategyPositiveInt(raw string, fallback int) (int, error) {
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return 0, errors.New("invalid positive integer")
	}
	return value, nil
}

func stockV2StrategyNonNegativeInt(raw string, fallback int) (int, error) {
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return 0, errors.New("invalid non-negative integer")
	}
	return value, nil
}
