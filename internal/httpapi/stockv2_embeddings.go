package httpapi

import (
	"encoding/json"
	"net/http"

	"phantom-lancer/internal/stockv2"
)

func (s *Server) handleStockV2EmbeddingStatus(w http.ResponseWriter, r *http.Request) {
	result, err := s.stockV2.GetEmbeddingStatus(r.Context())
	if err != nil {
		http.Error(w, err.Error(), stockV2AgentHTTPStatus(err))
		return
	}
	s.writeJSON(w, result)
}

func (s *Server) handleStockV2UpdateEmbeddingConfig(w http.ResponseWriter, r *http.Request) {
	var req stockv2.RequestUpdateEmbeddingConfig
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	result, err := s.stockV2.UpdateEmbeddingConfig(r.Context(), req)
	if err != nil {
		http.Error(w, err.Error(), stockV2AgentHTTPStatus(err))
		return
	}
	s.writeJSON(w, result)
}

func (s *Server) handleStockV2RebuildEmbeddings(w http.ResponseWriter, r *http.Request) {
	var req stockv2.RequestRebuildEmbeddingAssets
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	result, err := s.stockV2.RebuildEmbeddingAssets(r.Context(), req)
	if err != nil {
		http.Error(w, err.Error(), stockV2AgentHTTPStatus(err))
		return
	}
	s.writeJSON(w, result)
}

func (s *Server) handleStockV2ListEmbeddingAssets(w http.ResponseWriter, r *http.Request) {
	limit, offset, err := stockV2AgentPage(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	query := r.URL.Query()
	filter := stockv2.EmbeddingAssetListFilter{
		ObjectType: query.Get("objectType"),
		ObjectID:   query.Get("objectId"),
		ModelID:    query.Get("modelId"),
		Status:     query.Get("status"),
		Limit:      limit,
		Offset:     offset,
	}
	items, err := s.stockV2.ListEmbeddingAssets(r.Context(), filter)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	total, err := s.stockV2.CountEmbeddingAssets(r.Context(), filter)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.writeJSON(w, map[string]any{"items": items, "total": total, "limit": limit, "offset": offset})
}
