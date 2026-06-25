package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"phantom-lancer/internal/stockv2"
)

func (s *Server) handleStockV2GetLatestQuotes(w http.ResponseWriter, r *http.Request) {
	symbols := parseStockV2QuoteSymbols(r.URL.Query().Get("symbols"))
	items, err := s.stockV2.GetLatestQuotes(r.Context(), symbols)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, stockv2.ErrQuoteSymbolsRequired) ||
			errors.Is(err, stockv2.ErrTooManyQuoteSymbols) ||
			errors.Is(err, stockv2.ErrInvalidQuoteSymbol) {
			status = http.StatusBadRequest
		}
		http.Error(w, err.Error(), status)
		return
	}

	s.writeJSON(w, stockv2.QuoteRefreshResult{
		Items:          items,
		RefreshedCount: 0,
		FailedCount:    0,
		FailedItems:    []stockv2.UpdateFailure{},
		FetchedAt:      time.Now(),
	})
}

func (s *Server) handleStockV2RefreshLatestQuotes(w http.ResponseWriter, r *http.Request) {
	var req stockv2.RequestRefreshLatestQuotes
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.TriggerSource == "" {
		req.TriggerSource = "web"
	}

	result, err := s.stockV2.RefreshLatestQuotes(r.Context(), req.Symbols, req.TriggerSource)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, stockv2.ErrQuoteSymbolsRequired) ||
			errors.Is(err, stockv2.ErrTooManyQuoteSymbols) ||
			errors.Is(err, stockv2.ErrInvalidQuoteSymbol) {
			status = http.StatusBadRequest
		}
		http.Error(w, err.Error(), status)
		return
	}
	s.writeJSON(w, result)
}

func (s *Server) handleStockV2GetQuoteRefreshState(w http.ResponseWriter, r *http.Request) {
	limit := 20
	if raw := r.URL.Query().Get("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value <= 0 || value > 200 {
			http.Error(w, "invalid limit", http.StatusBadRequest)
			return
		}
		limit = value
	}
	state, items, err := s.stockV2.GetLatestQuoteRefreshState(r.Context(), limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.writeJSON(w, map[string]any{"state": state, "items": items})
}

func (s *Server) handleStockV2ListMinuteBars(w http.ResponseWriter, r *http.Request) {
	symbol := strings.TrimSpace(r.URL.Query().Get("symbol"))
	days := 5
	if raw := r.URL.Query().Get("days"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value <= 0 || value > 5 {
			http.Error(w, "invalid days", http.StatusBadRequest)
			return
		}
		days = value
	}
	limit := 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value <= 0 || value > 5000 {
			http.Error(w, "invalid limit", http.StatusBadRequest)
			return
		}
		limit = value
	}
	items, err := s.stockV2.ListMinuteBars(r.Context(), symbol, days, limit)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, stockv2.ErrInvalidQuoteSymbol) {
			status = http.StatusBadRequest
		}
		http.Error(w, err.Error(), status)
		return
	}
	s.writeJSON(w, map[string]any{"items": items, "days": days, "limit": limit})
}

func parseStockV2QuoteSymbols(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	symbols := make([]string, 0, len(parts))
	for _, part := range parts {
		if symbol := strings.TrimSpace(part); symbol != "" {
			symbols = append(symbols, symbol)
		}
	}
	return symbols
}
