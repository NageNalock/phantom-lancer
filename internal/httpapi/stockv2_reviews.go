package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"phantom-lancer/internal/stockv2"
)

func (s *Server) handleStockV2CreateReviewFromMonitorHit(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "monitor hit ID is required", http.StatusBadRequest)
		return
	}
	result, err := s.stockV2.CreateReviewFromMonitorHit(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), stockV2ReviewHTTPStatus(err))
		return
	}
	s.writeJSON(w, result)
}

func (s *Server) handleStockV2ListOperationReviews(w http.ResponseWriter, r *http.Request) {
	filter, err := stockV2ReviewFilterFromRequest(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	items, err := s.stockV2.ListOperationReviews(r.Context(), filter)
	if err != nil {
		http.Error(w, err.Error(), stockV2ReviewHTTPStatus(err))
		return
	}
	total, err := s.stockV2.CountOperationReviews(r.Context(), filter)
	if err != nil {
		http.Error(w, err.Error(), stockV2ReviewHTTPStatus(err))
		return
	}
	s.writeJSON(w, map[string]any{"items": items, "total": total, "limit": filter.Limit, "offset": filter.Offset})
}

func (s *Server) handleStockV2GetOperationReview(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "review ID is required", http.StatusBadRequest)
		return
	}
	result, err := s.stockV2.GetOperationReview(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), stockV2ReviewHTTPStatus(err))
		return
	}
	s.writeJSON(w, result)
}

func (s *Server) handleStockV2SaveOperationReviewResult(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "review ID is required", http.StatusBadRequest)
		return
	}
	var req stockv2.RequestSaveOperationReviewResult
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	result, err := s.stockV2.SaveOperationReviewResult(r.Context(), id, req)
	if err != nil {
		http.Error(w, err.Error(), stockV2ReviewHTTPStatus(err))
		return
	}
	s.writeJSON(w, result)
}

func stockV2ReviewFilterFromRequest(r *http.Request) (stockv2.OperationReviewListFilter, error) {
	query := r.URL.Query()
	limit, err := stockV2StrategyPositiveInt(query.Get("limit"), 50)
	if err != nil || limit > 200 {
		return stockv2.OperationReviewListFilter{}, errors.New("invalid limit")
	}
	offset, err := stockV2StrategyNonNegativeInt(query.Get("offset"), 0)
	if err != nil {
		return stockv2.OperationReviewListFilter{}, errors.New("invalid offset")
	}
	status := query.Get("status")
	if status != "" && !stockV2HTTPValidReviewStatus(status) {
		return stockv2.OperationReviewListFilter{}, errors.New("invalid review status")
	}
	outputType := query.Get("outputType")
	if outputType != "" && !stockV2HTTPValidReviewOutputType(outputType) {
		return stockv2.OperationReviewListFilter{}, errors.New("invalid review output type")
	}
	return stockv2.OperationReviewListFilter{
		Status:      status,
		OutputType:  outputType,
		HitID:       query.Get("hitId"),
		RunID:       query.Get("runId"),
		StrategyID:  query.Get("strategyId"),
		PortfolioID: query.Get("portfolioId"),
		Symbol:      query.Get("symbol"),
		Limit:       limit,
		Offset:      offset,
	}, nil
}

func stockV2ReviewHTTPStatus(err error) int {
	switch {
	case errors.Is(err, stockv2.ErrOperationReviewNotFound),
		errors.Is(err, stockv2.ErrMonitorHitNotFound):
		return http.StatusNotFound
	case errors.Is(err, stockv2.ErrInvalidOperationReviewStatus),
		errors.Is(err, stockv2.ErrInvalidOperationReviewOutputType),
		errors.Is(err, stockv2.ErrInvalidProposedOperation):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

func stockV2HTTPValidReviewStatus(status string) bool {
	return status == stockv2.OperationReviewStatusPending ||
		status == stockv2.OperationReviewStatusRunning ||
		status == stockv2.OperationReviewStatusCompleted ||
		status == stockv2.OperationReviewStatusFailed ||
		status == stockv2.OperationReviewStatusClosed
}

func stockV2HTTPValidReviewOutputType(outputType string) bool {
	return outputType == stockv2.OperationReviewOutputTradeSignal ||
		outputType == stockv2.OperationReviewOutputProposedOperation ||
		outputType == stockv2.OperationReviewOutputStrategyPatch ||
		outputType == stockv2.OperationReviewOutputIgnore ||
		outputType == stockv2.OperationReviewOutputContinueMonitoring
}
