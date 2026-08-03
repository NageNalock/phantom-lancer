package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"phantom-lancer/internal/stockv2"
)

func (s *Server) handleStockV2GetPortfolioSentinelConfig(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.stockV2.GetPortfolioSentinelConfig(r.Context())
	if err != nil {
		writeError(w, stockV2PortfolioSentinelHTTPStatus(err), "stockv2_portfolio_sentinel_config_failed", err.Error())
		return
	}
	s.writeJSON(w, cfg)
}

func (s *Server) handleStockV2UpdatePortfolioSentinelConfig(w http.ResponseWriter, r *http.Request) {
	var req stockv2.RequestUpdatePortfolioSentinelConfig
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_body", "invalid request body")
		return
	}
	cfg, err := s.stockV2.UpdatePortfolioSentinelConfig(r.Context(), req)
	if err != nil {
		writeError(w, stockV2PortfolioSentinelHTTPStatus(err), "stockv2_portfolio_sentinel_config_failed", err.Error())
		return
	}
	s.writeJSON(w, cfg)
}

func (s *Server) handleStockV2RunPortfolioSentinel(w http.ResponseWriter, r *http.Request) {
	var req stockv2.RequestRunPortfolioSentinel
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_body", "invalid request body")
		return
	}
	run, err := s.stockV2.RunPortfolioSentinel(r.Context(), req)
	if err != nil {
		writeError(w, stockV2PortfolioSentinelHTTPStatus(err), "stockv2_portfolio_sentinel_run_failed", err.Error())
		return
	}
	s.writeJSON(w, run)
}

func (s *Server) handleStockV2ListPortfolioSentinelRuns(w http.ResponseWriter, r *http.Request) {
	filter, err := stockV2PortfolioSentinelRunFilterFromRequest(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_filter", err.Error())
		return
	}
	items, err := s.stockV2.ListPortfolioSentinelRuns(r.Context(), filter)
	if err != nil {
		writeError(w, stockV2PortfolioSentinelHTTPStatus(err), "stockv2_portfolio_sentinel_list_failed", err.Error())
		return
	}
	total, err := s.stockV2.CountPortfolioSentinelRuns(r.Context(), filter)
	if err != nil {
		writeError(w, stockV2PortfolioSentinelHTTPStatus(err), "stockv2_portfolio_sentinel_count_failed", err.Error())
		return
	}
	s.writeJSON(w, map[string]any{"items": items, "total": total, "limit": filter.Limit, "offset": filter.Offset})
}

func (s *Server) handleStockV2GetPortfolioSentinelRun(w http.ResponseWriter, r *http.Request) {
	detail, err := s.stockV2.GetPortfolioSentinelRunDetail(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, stockV2PortfolioSentinelHTTPStatus(err), "stockv2_portfolio_sentinel_get_failed", err.Error())
		return
	}
	s.writeJSON(w, detail)
}

func (s *Server) handleStockV2GetPortfolioSentinelResult(w http.ResponseWriter, r *http.Request) {
	result, err := s.stockV2.GetPortfolioSentinelResult(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, stockV2PortfolioSentinelHTTPStatus(err), "stockv2_portfolio_sentinel_result_get_failed", err.Error())
		return
	}
	s.writeJSON(w, result)
}

func (s *Server) handleStockV2ListPortfolioSentinelActionPlans(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	action := query.Get("action")
	if action != "" && !stockV2HTTPValueIn(action,
		stockv2.PortfolioSentinelPlanBuild,
		stockv2.PortfolioSentinelPlanAdd,
		stockv2.PortfolioSentinelPlanHold,
		stockv2.PortfolioSentinelPlanReduce,
		stockv2.PortfolioSentinelPlanExit,
	) {
		writeError(w, http.StatusBadRequest, "invalid_filter", "invalid portfolio sentinel action")
		return
	}
	items, err := s.stockV2.ListPortfolioSentinelActionPlans(r.Context(), stockv2.PortfolioSentinelActionPlanListFilter{
		PortfolioID:    query.Get("portfolioId"),
		Action:         action,
		IncludeExpired: query.Get("includeExpired") == "true",
	})
	if err != nil {
		writeError(w, stockV2PortfolioSentinelHTTPStatus(err), "stockv2_portfolio_sentinel_action_plans_failed", err.Error())
		return
	}
	s.writeJSON(w, map[string]any{"items": items, "total": len(items)})
}

func stockV2PortfolioSentinelRunFilterFromRequest(r *http.Request) (stockv2.PortfolioSentinelRunListFilter, error) {
	query := r.URL.Query()
	limit, err := stockV2PositiveInt(query.Get("limit"), 50)
	if err != nil {
		return stockv2.PortfolioSentinelRunListFilter{}, errors.New("invalid limit")
	}
	offset, err := stockV2NonNegativeInt(query.Get("offset"), 0)
	if err != nil {
		return stockv2.PortfolioSentinelRunListFilter{}, errors.New("invalid offset")
	}
	status := query.Get("status")
	if status != "" && !stockV2HTTPValueIn(status, stockv2.PortfolioSentinelStatusRunning, stockv2.PortfolioSentinelStatusCompleted, stockv2.PortfolioSentinelStatusFailed) {
		return stockv2.PortfolioSentinelRunListFilter{}, errors.New("invalid portfolio sentinel status")
	}
	triggerType := query.Get("triggerType")
	if triggerType != "" && !stockV2HTTPValueIn(triggerType, stockv2.PortfolioSentinelTriggerManual, stockv2.PortfolioSentinelTriggerScheduled) {
		return stockv2.PortfolioSentinelRunListFilter{}, errors.New("invalid portfolio sentinel trigger type")
	}
	windowType := query.Get("windowType")
	if windowType != "" && !stockV2HTTPValueIn(windowType, stockv2.PortfolioSentinelWindowManual, stockv2.PortfolioSentinelWindowPreMarket, stockv2.PortfolioSentinelWindowMidday, stockv2.PortfolioSentinelWindowPostClose) {
		return stockv2.PortfolioSentinelRunListFilter{}, errors.New("invalid portfolio sentinel window type")
	}
	return stockv2.PortfolioSentinelRunListFilter{
		Status:      status,
		TriggerType: triggerType,
		WindowType:  windowType,
		PortfolioID: query.Get("portfolioId"),
		Limit:       limit,
		Offset:      offset,
	}, nil
}

func stockV2PortfolioSentinelHTTPStatus(err error) int {
	switch {
	case errors.Is(err, stockv2.ErrPortfolioSentinelRunNotFound),
		errors.Is(err, stockv2.ErrPortfolioSentinelResultNotFound),
		errors.Is(err, stockv2.ErrPortfolioNotFound):
		return http.StatusNotFound
	case errors.Is(err, stockv2.ErrPortfolioSentinelAlreadyRunning):
		return http.StatusConflict
	case errors.Is(err, stockv2.ErrInvalidPortfolioSentinelInput),
		errors.Is(err, stockv2.ErrInvalidPortfolioSentinelStatus),
		errors.Is(err, stockv2.ErrInvalidPortfolioSentinelResult):
		return http.StatusBadRequest
	case errors.Is(err, stockv2.ErrAgentModelNotAvailable),
		errors.Is(err, stockv2.ErrAgentTaskProfileNotFound),
		errors.Is(err, stockv2.ErrAgentTaskRequiresCLI),
		errors.Is(err, stockv2.ErrAgentExecutionModeModelMismatch),
		errors.Is(err, stockv2.ErrAgentExecutorUnavailable):
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}
