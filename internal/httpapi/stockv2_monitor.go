package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"phantom-lancer/internal/stockv2"
)

// 监控与任务 HTTP 层。watch/alert 仍保留底层路由,前端不再暴露「新建盯盘」入口。

func (s *Server) handleStockV2ListMonitorTasks(w http.ResponseWriter, r *http.Request) {
	items, err := s.stockV2.ListMonitorTasks(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.writeJSON(w, map[string]any{"items": items})
}

func (s *Server) handleStockV2UpdateMonitorTaskConfig(w http.ResponseWriter, r *http.Request) {
	taskType := r.PathValue("taskType")
	if taskType == "" {
		http.Error(w, "task type is required", http.StatusBadRequest)
		return
	}
	var req stockv2.RequestUpdateMonitorTaskConfig
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	result, err := s.stockV2.UpdateMonitorTaskConfig(r.Context(), taskType, req)
	if err != nil {
		http.Error(w, err.Error(), stockV2MonitorHTTPStatus(err))
		return
	}
	s.writeJSON(w, result)
}

func (s *Server) handleStockV2RunMonitorTask(w http.ResponseWriter, r *http.Request) {
	taskType := r.PathValue("taskType")
	if taskType == "" {
		http.Error(w, "task type is required", http.StatusBadRequest)
		return
	}
	result, err := s.stockV2.RunMonitorTask(r.Context(), taskType, stockv2.MonitorTriggerManual)
	if err != nil {
		http.Error(w, err.Error(), stockV2MonitorHTTPStatus(err))
		return
	}
	s.writeJSON(w, result)
}

func (s *Server) handleStockV2ListMonitorRuns(w http.ResponseWriter, r *http.Request) {
	filter, err := stockV2MonitorRunFilterFromRequest(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	items, err := s.stockV2.ListMonitorRuns(r.Context(), filter)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	total, err := s.stockV2.CountMonitorRuns(r.Context(), filter)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.writeJSON(w, map[string]any{"items": items, "total": total, "limit": filter.Limit, "offset": filter.Offset})
}

func (s *Server) handleStockV2GetMonitorRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "run ID is required", http.StatusBadRequest)
		return
	}
	result, err := s.stockV2.GetMonitorRun(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), stockV2MonitorHTTPStatus(err))
		return
	}
	s.writeJSON(w, result)
}

func (s *Server) handleStockV2ListMonitorRunAgentRuns(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "run ID is required", http.StatusBadRequest)
		return
	}
	items, err := s.stockV2.ListMonitorRunAgentExecutionDetails(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), stockV2MonitorHTTPStatus(err))
		return
	}
	s.writeJSON(w, map[string]any{"items": items, "total": len(items), "limit": len(items), "offset": 0})
}

func (s *Server) handleStockV2ListMonitorHits(w http.ResponseWriter, r *http.Request) {
	filter, err := stockV2MonitorHitFilterFromRequest(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	items, err := s.stockV2.ListMonitorHits(r.Context(), filter)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	total, err := s.stockV2.CountMonitorHits(r.Context(), filter)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.writeJSON(w, map[string]any{"items": items, "total": total, "limit": filter.Limit, "offset": filter.Offset})
}

func (s *Server) handleStockV2GetMonitorHit(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "hit ID is required", http.StatusBadRequest)
		return
	}
	result, err := s.stockV2.GetMonitorHit(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), stockV2MonitorHTTPStatus(err))
		return
	}
	s.writeJSON(w, result)
}

func stockV2MonitorRunFilterFromRequest(r *http.Request) (stockv2.MonitorRunListFilter, error) {
	query := r.URL.Query()
	limit, err := stockV2PositiveInt(query.Get("limit"), 50)
	if err != nil || limit > 200 {
		return stockv2.MonitorRunListFilter{}, errors.New("invalid limit")
	}
	offset, err := stockV2NonNegativeInt(query.Get("offset"), 0)
	if err != nil {
		return stockv2.MonitorRunListFilter{}, errors.New("invalid offset")
	}
	status := query.Get("status")
	if status != "" && !stockV2HTTPValidMonitorRunStatus(status) {
		return stockv2.MonitorRunListFilter{}, errors.New("invalid monitor run status")
	}
	return stockv2.MonitorRunListFilter{
		TaskType: query.Get("taskType"),
		Status:   status,
		Limit:    limit,
		Offset:   offset,
	}, nil
}

func stockV2MonitorHitFilterFromRequest(r *http.Request) (stockv2.MonitorHitListFilter, error) {
	query := r.URL.Query()
	limit, err := stockV2PositiveInt(query.Get("limit"), 50)
	if err != nil || limit > 200 {
		return stockv2.MonitorHitListFilter{}, errors.New("invalid limit")
	}
	offset, err := stockV2NonNegativeInt(query.Get("offset"), 0)
	if err != nil {
		return stockv2.MonitorHitListFilter{}, errors.New("invalid offset")
	}
	status := query.Get("status")
	if status != "" && !stockV2HTTPValidMonitorHitStatus(status) {
		return stockv2.MonitorHitListFilter{}, errors.New("invalid monitor hit status")
	}
	return stockv2.MonitorHitListFilter{
		RunID:       query.Get("runId"),
		TaskType:    query.Get("taskType"),
		Status:      status,
		StrategyID:  query.Get("strategyId"),
		PortfolioID: query.Get("portfolioId"),
		Symbol:      query.Get("symbol"),
		Limit:       limit,
		Offset:      offset,
	}, nil
}

func stockV2MonitorHTTPStatus(err error) int {
	switch {
	case errors.Is(err, stockv2.ErrMonitorTaskNotFound),
		errors.Is(err, stockv2.ErrMonitorHitNotFound):
		return http.StatusNotFound
	case errors.Is(err, stockv2.ErrMonitorTaskAlreadyRunning):
		return http.StatusConflict
	case errors.Is(err, stockv2.ErrMonitorTaskNotConfigured),
		errors.Is(err, stockv2.ErrInvalidMonitorTaskType):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

func stockV2HTTPValidMonitorRunStatus(status string) bool {
	return stockV2HTTPValueIn(status, stockv2.MonitorRunStatusRunning, stockv2.MonitorRunStatusCompleted, stockv2.MonitorRunStatusFailed, stockv2.MonitorRunStatusCancelled)
}

func stockV2HTTPValidMonitorHitStatus(status string) bool {
	return stockV2HTTPValueIn(status, stockv2.MonitorHitStatusCandidate, stockv2.MonitorHitStatusDoublechecked, stockv2.MonitorHitStatusAlerted, stockv2.MonitorHitStatusReviewed, stockv2.MonitorHitStatusIgnored)
}
