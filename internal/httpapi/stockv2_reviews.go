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

func (s *Server) handleStockV2AcceptOperationReview(w http.ResponseWriter, r *http.Request) {
	s.handleStockV2ReviewAction(w, r, "accept")
}

func (s *Server) handleStockV2RejectOperationReview(w http.ResponseWriter, r *http.Request) {
	s.handleStockV2ReviewAction(w, r, "reject")
}

func (s *Server) handleStockV2DeferOperationReview(w http.ResponseWriter, r *http.Request) {
	s.handleStockV2ReviewAction(w, r, "defer")
}

func (s *Server) handleStockV2ReviewAction(w http.ResponseWriter, r *http.Request, action string) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "review ID is required", http.StatusBadRequest)
		return
	}
	var req stockv2.RequestApplyOperationReviewAction
	if r.Body != nil && r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
	}
	var (
		result stockv2.OperationReview
		err    error
	)
	switch action {
	case "accept":
		result, err = s.stockV2.AcceptOperationReview(r.Context(), id, req)
	case "reject":
		result, err = s.stockV2.RejectOperationReview(r.Context(), id, req)
	case "defer":
		result, err = s.stockV2.DeferOperationReview(r.Context(), id, req)
	default:
		err = stockv2.ErrInvalidOperationReviewAction
	}
	if err != nil {
		http.Error(w, err.Error(), stockV2ReviewHTTPStatus(err))
		return
	}
	s.writeJSON(w, result)
}

func stockV2ReviewFilterFromRequest(r *http.Request) (stockv2.OperationReviewListFilter, error) {
	query := r.URL.Query()
	limit, err := stockV2PositiveInt(query.Get("limit"), 50)
	if err != nil || limit > 200 {
		return stockv2.OperationReviewListFilter{}, errors.New("invalid limit")
	}
	offset, err := stockV2NonNegativeInt(query.Get("offset"), 0)
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
		errors.Is(err, stockv2.ErrMonitorHitNotFound),
		errors.Is(err, stockv2.ErrStrategyNotFound),
		errors.Is(err, stockv2.ErrStrategyVersionNotFound),
		errors.Is(err, stockv2.ErrPortfolioNotFound):
		return http.StatusNotFound
	case errors.Is(err, stockv2.ErrInvalidOperationReviewStatus),
		errors.Is(err, stockv2.ErrInvalidOperationReviewOutputType),
		errors.Is(err, stockv2.ErrInvalidProposedOperation),
		errors.Is(err, stockv2.ErrInvalidOperationReviewAction),
		errors.Is(err, stockv2.ErrInvalidStrategyDirection),
		errors.Is(err, stockv2.ErrInvalidTransactionSide),
		errors.Is(err, stockv2.ErrInsufficientHolding):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

func stockV2HTTPValidReviewStatus(status string) bool {
	return stockV2HTTPValueIn(status, stockv2.OperationReviewStatusPending, stockv2.OperationReviewStatusRunning, stockv2.OperationReviewStatusCompleted, stockv2.OperationReviewStatusFailed, stockv2.OperationReviewStatusClosed)
}

func stockV2HTTPValidReviewOutputType(outputType string) bool {
	return stockV2HTTPValueIn(outputType, stockv2.OperationReviewOutputTradeSignal, stockv2.OperationReviewOutputProposedOperation, stockv2.OperationReviewOutputStrategyPatch, stockv2.OperationReviewOutputIgnore, stockv2.OperationReviewOutputContinueMonitoring)
}

// handleStockV2RunAgentReview 对某个 Review 启动 Agent 运行。
func (s *Server) handleStockV2RunAgentReview(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "review ID is required", http.StatusBadRequest)
		return
	}

	var req struct {
		RequestedBy string `json:"requestedBy,omitempty"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
	}

	run, err := s.stockV2.RunAgentReviewForReview(r.Context(), id, req.RequestedBy)
	if err != nil {
		http.Error(w, err.Error(), stockV2AgentHTTPStatus(err))
		return
	}
	s.writeJSON(w, run)
}
