package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"phantom-lancer/internal/stockv2"
)

func (s *Server) handleStockV2ListStockProfiles(w http.ResponseWriter, r *http.Request) {
	filter, err := stockV2ProfileFilterFromRequest(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	items, err := s.stockV2.ListStockProfiles(r.Context(), filter)
	if err != nil {
		http.Error(w, err.Error(), stockV2ProfileHTTPStatus(err))
		return
	}
	total, err := s.stockV2.CountStockProfiles(r.Context(), filter)
	if err != nil {
		http.Error(w, err.Error(), stockV2ProfileHTTPStatus(err))
		return
	}
	s.writeJSON(w, map[string]any{"items": items, "total": total, "limit": filter.Limit, "offset": filter.Offset})
}

func (s *Server) handleStockV2GetStockProfile(w http.ResponseWriter, r *http.Request) {
	symbol := r.PathValue("symbol")
	if symbol == "" {
		http.Error(w, "symbol is required", http.StatusBadRequest)
		return
	}
	profile, err := s.stockV2.GetStockProfile(r.Context(), symbol)
	if err != nil {
		http.Error(w, err.Error(), stockV2ProfileHTTPStatus(err))
		return
	}
	s.writeJSON(w, profile)
}

func (s *Server) handleStockV2ListStockProfileSummaries(w http.ResponseWriter, r *http.Request) {
	raw := r.URL.Query().Get("symbols")
	if strings.TrimSpace(raw) == "" {
		s.writeJSON(w, map[string]any{"items": map[string]stockv2.StockProfileSummary{}})
		return
	}
	parts := strings.Split(raw, ",")
	if len(parts) > 200 {
		parts = parts[:200]
	}
	items, err := s.stockV2.ListStockProfileSummaries(r.Context(), parts)
	if err != nil {
		http.Error(w, err.Error(), stockV2ProfileHTTPStatus(err))
		return
	}
	s.writeJSON(w, map[string]any{"items": items})
}

func (s *Server) handleStockV2BuildStockProfile(w http.ResponseWriter, r *http.Request) {
	symbol := r.PathValue("symbol")
	if symbol == "" {
		http.Error(w, "symbol is required", http.StatusBadRequest)
		return
	}
	profile, err := s.stockV2.BuildStockProfile(r.Context(), symbol)
	if err != nil {
		http.Error(w, err.Error(), stockV2ProfileHTTPStatus(err))
		return
	}
	s.writeJSON(w, profile)
}

func (s *Server) handleStockV2RebuildStockProfiles(w http.ResponseWriter, r *http.Request) {
	result, err := s.stockV2.RebuildStockProfiles(r.Context())
	if err != nil {
		http.Error(w, err.Error(), stockV2ProfileHTTPStatus(err))
		return
	}
	s.writeJSON(w, result)
}

func (s *Server) handleStockV2RunStockProfileAgent(w http.ResponseWriter, r *http.Request) {
	symbol := r.PathValue("symbol")
	if symbol == "" {
		http.Error(w, "symbol is required", http.StatusBadRequest)
		return
	}
	var req struct {
		RequestedBy string `json:"requestedBy,omitempty"`
	}
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
	}
	run, err := s.stockV2.RunAgentStockProfileSummary(r.Context(), symbol, req.RequestedBy)
	if err != nil {
		http.Error(w, err.Error(), stockV2ProfileHTTPStatus(err))
		return
	}
	s.writeJSON(w, run)
}

func stockV2ProfileFilterFromRequest(r *http.Request) (stockv2.StockProfileListFilter, error) {
	query := r.URL.Query()
	limit, err := stockV2StrategyPositiveInt(query.Get("limit"), 50)
	if err != nil || limit > 500 {
		return stockv2.StockProfileListFilter{}, errors.New("invalid limit")
	}
	offset, err := stockV2StrategyNonNegativeInt(query.Get("offset"), 0)
	if err != nil {
		return stockv2.StockProfileListFilter{}, errors.New("invalid offset")
	}
	instrumentType := query.Get("instrumentType")
	if instrumentType != "" && instrumentType != stockv2.InstrumentTypeStock && instrumentType != stockv2.InstrumentTypeExchangeFund {
		return stockv2.StockProfileListFilter{}, errors.New("invalid instrument type")
	}
	return stockv2.StockProfileListFilter{
		Market:         query.Get("market"),
		InstrumentType: instrumentType,
		Keyword:        query.Get("keyword"),
		Limit:          limit,
		Offset:         offset,
	}, nil
}

func stockV2ProfileHTTPStatus(err error) int {
	switch {
	case errors.Is(err, stockv2.ErrStockProfileNotFound), errors.Is(err, stockv2.ErrInstrumentNotFound):
		return http.StatusNotFound
	case errors.Is(err, stockv2.ErrAgentExecutorUnavailable),
		errors.Is(err, stockv2.ErrAgentModelNotAvailable),
		errors.Is(err, stockv2.ErrAgentTaskProfileNotFound),
		errors.Is(err, stockv2.ErrAgentTaskNotConfigurable):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}
