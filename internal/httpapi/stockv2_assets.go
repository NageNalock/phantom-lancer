package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"phantom-lancer/internal/stockv2"
)

func (s *Server) handleStockV2ListUpdateJobItems(w http.ResponseWriter, r *http.Request) {
	jobID := strings.TrimSpace(r.PathValue("jobId"))
	if jobID == "" {
		http.Error(w, "job ID is required", http.StatusBadRequest)
		return
	}
	limit, err := stockV2PositiveInt(r.URL.Query().Get("limit"), 200)
	if err != nil || limit > 500 {
		http.Error(w, "invalid limit", http.StatusBadRequest)
		return
	}
	offset, err := stockV2NonNegativeInt(r.URL.Query().Get("offset"), 0)
	if err != nil {
		http.Error(w, "invalid offset", http.StatusBadRequest)
		return
	}
	filter := stockv2.AssetMaintenanceItemListFilter{JobID: jobID, Limit: limit, Offset: offset}
	items, err := s.stockV2.ListAssetMaintenanceItems(r.Context(), filter)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	total, err := s.stockV2.CountAssetMaintenanceItems(r.Context(), filter)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.writeJSON(w, map[string]any{"items": items, "total": total, "limit": limit, "offset": offset})
}

func (s *Server) handleStockV2ListAssetSummaries(w http.ResponseWriter, r *http.Request) {
	raw := strings.TrimSpace(r.URL.Query().Get("symbols"))
	if raw == "" {
		s.writeJSON(w, map[string]any{"items": map[string]stockv2.StockV2AssetSummary{}})
		return
	}
	parts := strings.Split(raw, ",")
	if len(parts) > 200 {
		parts = parts[:200]
	}
	items, err := s.stockV2.ListAssetSummaries(r.Context(), parts)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.writeJSON(w, map[string]any{"items": items})
}

func (s *Server) handleStockV2ListAnnouncements(w http.ResponseWriter, r *http.Request) {
	limit, err := stockV2PositiveInt(r.URL.Query().Get("limit"), 50)
	if err != nil || limit > 200 {
		http.Error(w, "invalid limit", http.StatusBadRequest)
		return
	}
	offset, err := stockV2NonNegativeInt(r.URL.Query().Get("offset"), 0)
	if err != nil {
		http.Error(w, "invalid offset", http.StatusBadRequest)
		return
	}
	majorOnly, err := parseOptionalBool(r.URL.Query().Get("majorOnly"))
	if err != nil {
		http.Error(w, "invalid majorOnly", http.StatusBadRequest)
		return
	}
	filter := stockv2.AnnouncementListFilter{
		Symbol:    strings.TrimSpace(r.URL.Query().Get("symbol")),
		MajorOnly: majorOnly,
		Limit:     limit,
		Offset:    offset,
	}
	items, err := s.stockV2.ListAnnouncements(r.Context(), filter)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	total, err := s.stockV2.CountAnnouncements(r.Context(), filter)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.writeJSON(w, map[string]any{"items": items, "total": total, "limit": limit, "offset": offset})
}

func (s *Server) handleStockV2MaintainAsset(w http.ResponseWriter, r *http.Request) {
	symbol := strings.TrimSpace(r.PathValue("symbol"))
	if symbol == "" {
		http.Error(w, "symbol is required", http.StatusBadRequest)
		return
	}
	var req stockv2.AssetMaintainSymbolRequest
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
	}
	result, err := s.stockV2.MaintainAssetForSymbol(r.Context(), symbol, req)
	if err != nil && result.Item.ID == "" {
		http.Error(w, err.Error(), stockV2ProfileHTTPStatus(err))
		return
	}
	s.writeJSON(w, result)
}

func parseOptionalBool(raw string) (bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false, nil
	}
	return strconv.ParseBool(raw)
}
