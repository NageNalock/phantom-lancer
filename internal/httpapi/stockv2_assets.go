package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"phantom-lancer/internal/stockv2"
)

const (
	stockV2AssetReadinessMaxSymbols = 500
	stockV2AssetReadinessBodyBytes  = 64 << 10
)

type stockV2AssetReadinessEvaluateRequest struct {
	Symbols []string `json:"symbols"`
}

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

func (s *Server) handleStockV2EvaluateAssetReadiness(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, stockV2AssetReadinessBodyBytes)
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var req stockV2AssetReadinessEvaluateRequest
	if err := decoder.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_body", "invalid request body")
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid_request_body", "invalid request body")
		return
	}
	if len(req.Symbols) == 0 {
		writeError(w, http.StatusBadRequest, "symbols_required", "symbols are required")
		return
	}
	if len(req.Symbols) > stockV2AssetReadinessMaxSymbols {
		writeError(w, http.StatusBadRequest, "too_many_symbols", "symbols must contain at most 500 items")
		return
	}
	symbols, err := stockv2.NormalizeAssetReadinessSymbols(req.Symbols)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_symbol", "symbols contain an invalid stock symbol")
		return
	}
	itemsBySymbol, err := s.stockV2.EvaluateAssetReadinessBatch(r.Context(), symbols, time.Time{})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "asset_readiness_evaluation_failed", "failed to evaluate asset readiness")
		return
	}
	items := make([]stockv2.UnifiedAssetReadiness, 0, len(symbols))
	for _, symbol := range symbols {
		items = append(items, itemsBySymbol[symbol])
	}
	s.writeJSON(w, map[string]any{"items": items, "count": len(items)})
}

func (s *Server) handleStockV2GetAssetReadinessOverview(w http.ResponseWriter, r *http.Request) {
	overview, err := s.stockV2.GetAssetReadinessOverview(r.Context(), time.Time{})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "asset_readiness_overview_failed", "failed to evaluate asset readiness overview")
		return
	}
	s.writeJSON(w, overview)
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
