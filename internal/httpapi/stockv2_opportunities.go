package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"phantom-lancer/internal/stockv2"
	"phantom-lancer/internal/storage"
)

func (s *Server) handleStockV2GetOpportunityDiscoveryConfig(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuth(w, r); !ok {
		return
	}
	item, err := s.stockV2.GetOpportunityDiscoveryConfig(r.Context())
	if err != nil {
		writeError(w, stockV2OpportunityHTTPStatus(err), "opportunity_discovery_config_failed", err.Error())
		return
	}
	s.writeJSON(w, item)
}

func (s *Server) handleStockV2UpdateOpportunityDiscoveryConfig(w http.ResponseWriter, r *http.Request) {
	session, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, session.Session) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
	var req stockv2.RequestUpdateOpportunityDiscoveryConfig
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_body", "invalid request body")
		return
	}
	item, err := s.stockV2.UpdateOpportunityDiscoveryConfig(r.Context(), req)
	if err != nil {
		writeError(w, stockV2OpportunityHTTPStatus(err), "opportunity_discovery_config_failed", err.Error())
		return
	}
	_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
		EventType: "stockv2_opportunity_discovery_config_updated",
		RiskLevel: "medium",
		Summary:   "更新机会发现标的范围",
		Payload: map[string]any{
			"ownerId":                     session.Session.OwnerID,
			"excludeChiNextAndStarMarket": item.ExcludeChiNextAndStarMarket,
		},
	})
	s.writeJSON(w, item)
}

func (s *Server) handleStockV2ListOpportunities(w http.ResponseWriter, r *http.Request) {
	filter, err := stockV2OpportunityFilterFromRequest(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	items, err := s.stockV2.ListOpportunities(r.Context(), filter)
	if err != nil {
		http.Error(w, err.Error(), stockV2OpportunityHTTPStatus(err))
		return
	}
	total, err := s.stockV2.CountOpportunities(r.Context(), filter)
	if err != nil {
		http.Error(w, err.Error(), stockV2OpportunityHTTPStatus(err))
		return
	}
	s.writeJSON(w, map[string]any{"items": items, "total": total, "limit": filter.Limit, "offset": filter.Offset})
}

func (s *Server) handleStockV2CreateOpportunity(w http.ResponseWriter, r *http.Request) {
	var req stockv2.RequestCreateOpportunity
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	item, err := s.stockV2.CreateOpportunity(r.Context(), req)
	if err != nil {
		http.Error(w, err.Error(), stockV2OpportunityHTTPStatus(err))
		return
	}
	s.writeJSON(w, item)
}

func (s *Server) handleStockV2GetOpportunity(w http.ResponseWriter, r *http.Request) {
	item, err := s.stockV2.GetOpportunity(r.Context(), r.PathValue("id"))
	if err != nil {
		http.Error(w, err.Error(), stockV2OpportunityHTTPStatus(err))
		return
	}
	s.writeJSON(w, item)
}

func (s *Server) handleStockV2UpdateOpportunity(w http.ResponseWriter, r *http.Request) {
	var req stockv2.RequestUpdateOpportunity
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	item, err := s.stockV2.UpdateOpportunity(r.Context(), r.PathValue("id"), req)
	if err != nil {
		http.Error(w, err.Error(), stockV2OpportunityHTTPStatus(err))
		return
	}
	s.writeJSON(w, item)
}

func (s *Server) handleStockV2DeleteOpportunity(w http.ResponseWriter, r *http.Request) {
	if err := s.stockV2.DeleteOpportunity(r.Context(), r.PathValue("id")); err != nil {
		http.Error(w, err.Error(), stockV2OpportunityHTTPStatus(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleStockV2StartOpportunityDiscoveryRun(w http.ResponseWriter, r *http.Request) {
	var req stockv2.RequestStartOpportunityDiscoveryRun
	if err := stockV2DecodeOptionalJSONBody(r, &req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	item, err := s.stockV2.StartOpportunityDiscoveryRun(r.Context(), r.PathValue("id"), req)
	if err != nil {
		http.Error(w, err.Error(), stockV2OpportunityHTTPStatus(err))
		return
	}
	s.writeJSON(w, item)
}

func (s *Server) handleStockV2ListOpportunityDiscoveryRuns(w http.ResponseWriter, r *http.Request) {
	limit, offset, err := stockV2AgentPage(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	filter := stockv2.DiscoveryRunListFilter{
		OpportunityID: r.PathValue("id"),
		Status:        r.URL.Query().Get("status"),
		Limit:         limit,
		Offset:        offset,
	}
	items, err := s.stockV2.ListOpportunityDiscoveryRuns(r.Context(), filter)
	if err != nil {
		http.Error(w, err.Error(), stockV2OpportunityHTTPStatus(err))
		return
	}
	total, err := s.stockV2.CountOpportunityDiscoveryRuns(r.Context(), filter)
	if err != nil {
		http.Error(w, err.Error(), stockV2OpportunityHTTPStatus(err))
		return
	}
	s.writeJSON(w, map[string]any{"items": items, "total": total, "limit": limit, "offset": offset})
}

func (s *Server) handleStockV2GetOpportunityDiscoveryRun(w http.ResponseWriter, r *http.Request) {
	item, err := s.stockV2.GetOpportunityDiscoveryRun(r.Context(), r.PathValue("id"))
	if err != nil {
		http.Error(w, err.Error(), stockV2OpportunityHTTPStatus(err))
		return
	}
	s.writeJSON(w, item)
}

func (s *Server) handleStockV2ListOpportunityDiscoverySteps(w http.ResponseWriter, r *http.Request) {
	limit, offset, err := stockV2AgentPage(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	filter := stockv2.DiscoveryStepListFilter{RunID: r.PathValue("id"), Status: r.URL.Query().Get("status"), Limit: limit, Offset: offset}
	items, err := s.stockV2.ListOpportunityDiscoverySteps(r.Context(), filter)
	if err != nil {
		http.Error(w, err.Error(), stockV2OpportunityHTTPStatus(err))
		return
	}
	total, err := s.stockV2.CountOpportunityDiscoverySteps(r.Context(), filter)
	if err != nil {
		http.Error(w, err.Error(), stockV2OpportunityHTTPStatus(err))
		return
	}
	s.writeJSON(w, map[string]any{"items": items, "total": total, "limit": limit, "offset": offset})
}

func (s *Server) handleStockV2ListOpportunityEvidence(w http.ResponseWriter, r *http.Request) {
	limit, offset, err := stockV2AgentPage(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	filter := stockv2.OpportunityEvidenceListFilter{
		RunID:       r.PathValue("id"),
		CandidateID: r.URL.Query().Get("candidateId"),
		SourceType:  r.URL.Query().Get("sourceType"),
		Limit:       limit,
		Offset:      offset,
	}
	items, err := s.stockV2.ListOpportunityEvidence(r.Context(), filter)
	if err != nil {
		http.Error(w, err.Error(), stockV2OpportunityHTTPStatus(err))
		return
	}
	total, err := s.stockV2.CountOpportunityEvidence(r.Context(), filter)
	if err != nil {
		http.Error(w, err.Error(), stockV2OpportunityHTTPStatus(err))
		return
	}
	s.writeJSON(w, map[string]any{"items": items, "total": total, "limit": limit, "offset": offset})
}

func (s *Server) handleStockV2ListOpportunityCandidates(w http.ResponseWriter, r *http.Request) {
	limit, offset, err := stockV2AgentPage(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	filter := stockv2.OpportunityCandidateListFilter{
		RunID:  r.PathValue("id"),
		Status: r.URL.Query().Get("status"),
		Symbol: r.URL.Query().Get("symbol"),
		Limit:  limit,
		Offset: offset,
	}
	items, err := s.stockV2.ListOpportunityCandidates(r.Context(), filter)
	if err != nil {
		http.Error(w, err.Error(), stockV2OpportunityHTTPStatus(err))
		return
	}
	total, err := s.stockV2.CountOpportunityCandidates(r.Context(), filter)
	if err != nil {
		http.Error(w, err.Error(), stockV2OpportunityHTTPStatus(err))
		return
	}
	s.writeJSON(w, map[string]any{"items": items, "total": total, "limit": limit, "offset": offset})
}

func (s *Server) handleStockV2GetOpportunityResult(w http.ResponseWriter, r *http.Request) {
	item, err := s.stockV2.GetOpportunityResultByRunID(r.Context(), r.PathValue("id"))
	if err != nil {
		http.Error(w, err.Error(), stockV2OpportunityHTTPStatus(err))
		return
	}
	s.writeJSON(w, item)
}

func (s *Server) handleStockV2UpdateOpportunityCandidate(w http.ResponseWriter, r *http.Request) {
	var req stockv2.RequestUpdateOpportunityCandidate
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	item, err := s.stockV2.UpdateOpportunityCandidate(r.Context(), r.PathValue("id"), req)
	if err != nil {
		http.Error(w, err.Error(), stockV2OpportunityHTTPStatus(err))
		return
	}
	s.writeJSON(w, item)
}

func (s *Server) handleStockV2GenerateStrategyFromOpportunityCandidate(w http.ResponseWriter, r *http.Request) {
	var req stockv2.RequestGenerateStrategyFromOpportunityCandidate
	if err := stockV2DecodeOptionalJSONBody(r, &req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	item, err := s.stockV2.GenerateStrategyFromOpportunityCandidate(r.Context(), r.PathValue("id"), req)
	if err != nil {
		http.Error(w, err.Error(), stockV2OpportunityHTTPStatus(err))
		return
	}
	s.writeJSON(w, item)
}

func (s *Server) handleStockV2GetEmbeddingStatus(w http.ResponseWriter, r *http.Request) {
	item, err := s.stockV2.GetEmbeddingStatus(r.Context())
	if err != nil {
		http.Error(w, err.Error(), stockV2OpportunityHTTPStatus(err))
		return
	}
	s.writeJSON(w, item)
}

func (s *Server) handleStockV2UpdateEmbeddingConfig(w http.ResponseWriter, r *http.Request) {
	var req stockv2.RequestUpdateEmbeddingConfig
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	item, err := s.stockV2.UpdateEmbeddingConfig(r.Context(), req)
	if err != nil {
		http.Error(w, err.Error(), stockV2OpportunityHTTPStatus(err))
		return
	}
	s.writeJSON(w, item)
}

func (s *Server) handleStockV2RebuildEmbeddings(w http.ResponseWriter, r *http.Request) {
	var req stockv2.RequestRebuildEmbeddingAssets
	if err := stockV2DecodeOptionalJSONBody(r, &req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	item, err := s.stockV2.RebuildEmbeddingAssets(r.Context(), req)
	if err != nil {
		http.Error(w, err.Error(), stockV2OpportunityHTTPStatus(err))
		return
	}
	s.writeJSON(w, item)
}

func (s *Server) handleStockV2ListEmbeddingAssets(w http.ResponseWriter, r *http.Request) {
	limit, offset, err := stockV2AgentPage(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	filter := stockv2.EmbeddingAssetListFilter{
		ObjectType: r.URL.Query().Get("objectType"),
		ObjectID:   r.URL.Query().Get("objectId"),
		ModelID:    r.URL.Query().Get("modelId"),
		Status:     r.URL.Query().Get("status"),
		Limit:      limit,
		Offset:     offset,
	}
	items, err := s.stockV2.ListEmbeddingAssets(r.Context(), filter)
	if err != nil {
		http.Error(w, err.Error(), stockV2OpportunityHTTPStatus(err))
		return
	}
	total, err := s.stockV2.CountEmbeddingAssets(r.Context(), filter)
	if err != nil {
		http.Error(w, err.Error(), stockV2OpportunityHTTPStatus(err))
		return
	}
	s.writeJSON(w, map[string]any{"items": items, "total": total, "limit": limit, "offset": offset})
}

func stockV2OpportunityFilterFromRequest(r *http.Request) (stockv2.OpportunityListFilter, error) {
	limit, offset, err := stockV2AgentPage(r)
	if err != nil {
		return stockv2.OpportunityListFilter{}, err
	}
	return stockv2.OpportunityListFilter{
		Status:          r.URL.Query().Get("status"),
		MarketScope:     r.URL.Query().Get("marketScope"),
		InstrumentScope: r.URL.Query().Get("instrumentScope"),
		Keyword:         firstNonEmptyStrategyQuery(r.URL.Query().Get("q"), r.URL.Query().Get("keyword")),
		Limit:           limit,
		Offset:          offset,
	}, nil
}

func stockV2DecodeOptionalJSONBody(r *http.Request, dst any) error {
	if r.Body == nil {
		return nil
	}
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func stockV2OpportunityHTTPStatus(err error) int {
	switch {
	case errors.Is(err, stockv2.ErrOpportunityNotFound),
		errors.Is(err, stockv2.ErrDiscoveryRunNotFound),
		errors.Is(err, stockv2.ErrDiscoveryStepNotFound),
		errors.Is(err, stockv2.ErrEvidenceNotFound),
		errors.Is(err, stockv2.ErrOpportunityCandidateNotFound),
		errors.Is(err, stockv2.ErrOpportunityResultNotFound),
		errors.Is(err, stockv2.ErrAgentModelNotFound),
		errors.Is(err, stockv2.ErrInstrumentNotFound):
		return http.StatusNotFound
	case errors.Is(err, stockv2.ErrAgentModelNotAvailable),
		errors.Is(err, stockv2.ErrAgentExecutorUnavailable),
		errors.Is(err, stockv2.ErrEmbeddingDisabled),
		errors.Is(err, stockv2.ErrEmbeddingModelNotConfigured),
		errors.Is(err, stockv2.ErrEmbeddingModelUnavailable),
		errors.Is(err, stockv2.ErrEmbeddingAssetNotReady),
		errors.Is(err, stockv2.ErrOpportunityCandidateOutOfScope):
		return http.StatusConflict
	case errors.Is(err, stockv2.ErrInvalidOpportunityInput),
		errors.Is(err, stockv2.ErrInvalidOpportunityStatus),
		errors.Is(err, stockv2.ErrInvalidDiscoveryRunStatus),
		errors.Is(err, stockv2.ErrInvalidDiscoveryStepStatus),
		errors.Is(err, stockv2.ErrInvalidOpportunityCandidate),
		errors.Is(err, stockv2.ErrInvalidOpportunityResult),
		errors.Is(err, stockv2.ErrOpportunityUnsafeResult),
		errors.Is(err, stockv2.ErrOpportunitySymbolNotFound),
		errors.Is(err, stockv2.ErrEmbeddingModelInvalid),
		errors.Is(err, stockv2.ErrEmbeddingDimensionsMismatch),
		errors.Is(err, stockv2.ErrInvalidStrategyGenerationInput):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}
