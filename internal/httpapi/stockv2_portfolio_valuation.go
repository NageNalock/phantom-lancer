package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"

	"phantom-lancer/internal/stockv2"
)

func (s *Server) handleStockV2RefreshPortfolioValuation(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "portfolio ID is required", http.StatusBadRequest)
		return
	}
	var req stockv2.RequestRefreshPortfolioValuation
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.TriggerSource == "" {
		req.TriggerSource = "web"
	}
	result, err := s.stockV2.RefreshPortfolioValuation(r.Context(), id, req.TriggerSource)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.writeJSON(w, result)
}

func (s *Server) handleStockV2GetPortfolioSnapshots(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "portfolio ID is required", http.StatusBadRequest)
		return
	}
	limit := 20
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	snapshots, err := s.stockV2.GetPortfolioSnapshots(r.Context(), id, limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.writeJSON(w, map[string]any{"items": snapshots, "limit": limit})
}
