package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"

	"phantom-lancer/internal/stockv2"
)

// RegisterStockV2Routes 注册股票V2相关的HTTP路由
func (s *Server) RegisterStockV2Routes(mux *http.ServeMux) {
	// V2 股票系统路由
	mux.HandleFunc("GET /api/stockv2/snapshot", s.handleStockV2Snapshot)

	// 投资组合管理
	mux.HandleFunc("GET /api/stockv2/portfolios", s.handleListPortfolios)
	mux.HandleFunc("POST /api/stockv2/portfolios", s.handleCreatePortfolio)
	mux.HandleFunc("GET /api/stockv2/portfolios/{id}", s.handleGetPortfolio)
	mux.HandleFunc("PUT /api/stockv2/portfolios/{id}", s.handleUpdatePortfolio)
	mux.HandleFunc("DELETE /api/stockv2/portfolios/{id}", s.handleDeletePortfolio)
	mux.HandleFunc("POST /api/stockv2/portfolios/{id}/holdings", s.handleCreateHolding)
	mux.HandleFunc("GET /api/stockv2/portfolios/{id}/holdings", s.handleListHoldings)
	mux.HandleFunc("PUT /api/stockv2/portfolios/{id}/holdings/{holdingId}", s.handleUpdateHolding)
	mux.HandleFunc("DELETE /api/stockv2/portfolios/{id}/holdings/{holdingId}", s.handleDeleteHolding)

	// 股票主数据
	mux.HandleFunc("GET /api/stockv2/instruments", s.handleListInstruments)
	mux.HandleFunc("GET /api/stockv2/instruments/market/{market}", s.handleGetInstrumentsByMarket)
	mux.HandleFunc("GET /api/stockv2/instruments/search", s.handleSearchInstruments)

	// 更新任务
	mux.HandleFunc("POST /api/stockv2/update/trigger", s.handleTriggerUpdate)
	mux.HandleFunc("GET /api/stockv2/update/status/{jobId}", s.handleGetUpdateStatus)
	mux.HandleFunc("GET /api/stockv2/update/latest", s.handleGetLatestUpdate)
	mux.HandleFunc("GET /api/stockv2/update/history", s.handleGetUpdateHistory)

	// 配置管理
	mux.HandleFunc("GET /api/stockv2/settings", s.handleStockV2GetSettings)
	mux.HandleFunc("PUT /api/stockv2/settings", s.handleStockV2UpdateSettings)
}

// handleStockV2Snapshot 处理 V2 工作台快照请求。
//
// 该接口用于 UI 首屏聚合上下文，不是股票主数据全量查询。主数据完整性、
// 总数和分页列表应使用 /api/stockv2/instruments。
func (s *Server) handleStockV2Snapshot(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	snapshot, err := s.stockV2.Snapshot(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.writeJSON(w, snapshot)
}

// handleCreatePortfolio 处理创建投资组合请求
func (s *Server) handleCreatePortfolio(w http.ResponseWriter, r *http.Request) {
	var req stockv2.RequestCreatePortfolio
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	portfolio, err := s.stockV2.CreatePortfolio(ctx, req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	s.writeJSON(w, portfolio)
}

// handleListPortfolios 处理列出投资组合请求
func (s *Server) handleListPortfolios(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	portfolios, err := s.stockV2.ListPortfolios(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.writeJSON(w, portfolios)
}

// handleGetPortfolio 处理获取投资组合请求
func (s *Server) handleGetPortfolio(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "portfolio ID is required", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	portfolio, err := s.stockV2.GetPortfolio(ctx, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	s.writeJSON(w, portfolio)
}

// handleUpdatePortfolio 处理更新投资组合请求
func (s *Server) handleUpdatePortfolio(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "portfolio ID is required", http.StatusBadRequest)
		return
	}

	var req stockv2.RequestUpdatePortfolio
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	portfolio, err := s.stockV2.UpdatePortfolio(ctx, id, req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	s.writeJSON(w, portfolio)
}

// handleDeletePortfolio 处理删除投资组合请求
func (s *Server) handleDeletePortfolio(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "portfolio ID is required", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	if err := s.stockV2.DeletePortfolio(ctx, id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.writeJSON(w, map[string]string{"message": "portfolio deleted"})
}

// handleCreateHolding 处理创建持仓请求
func (s *Server) handleCreateHolding(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "portfolio ID is required", http.StatusBadRequest)
		return
	}

	var req stockv2.RequestCreateHolding
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	holding, err := s.stockV2.CreateHolding(ctx, id, req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	s.writeJSON(w, holding)
}

// handleListHoldings 处理列出持仓请求
func (s *Server) handleListHoldings(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "portfolio ID is required", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	holdings, err := s.stockV2.ListHoldings(ctx, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.writeJSON(w, holdings)
}

// handleUpdateHolding 处理更新持仓请求
func (s *Server) handleUpdateHolding(w http.ResponseWriter, r *http.Request) {
	portfolioID := r.PathValue("id")
	holdingID := r.PathValue("holdingId")
	if portfolioID == "" || holdingID == "" {
		http.Error(w, "portfolio ID and holding ID are required", http.StatusBadRequest)
		return
	}

	var req stockv2.RequestUpdateHolding
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	holding, err := s.stockV2.UpdateHolding(ctx, portfolioID, holdingID, req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	s.writeJSON(w, holding)
}

// handleDeleteHolding 处理删除持仓请求
func (s *Server) handleDeleteHolding(w http.ResponseWriter, r *http.Request) {
	portfolioID := r.PathValue("id")
	holdingID := r.PathValue("holdingId")
	if portfolioID == "" || holdingID == "" {
		http.Error(w, "portfolio ID and holding ID are required", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	if err := s.stockV2.DeleteHolding(ctx, portfolioID, holdingID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.writeJSON(w, map[string]string{"message": "holding deleted"})
}

// handleListInstruments 处理列出股票主数据请求
func (s *Server) handleListInstruments(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// 解析查询参数
	limitStr := r.URL.Query().Get("limit")
	offsetStr := r.URL.Query().Get("offset")

	limit := 100 // 默认值
	offset := 0  // 默认值

	if limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 && l <= 1000 {
			limit = l
		}
	}

	if offsetStr != "" {
		if o, err := strconv.Atoi(offsetStr); err == nil && o >= 0 {
			offset = o
		}
	}

	instruments, err := s.stockV2.GetInstruments(ctx, limit, offset)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	total, err := s.stockV2.CountInstruments(ctx)
	if err != nil {
		// 计数失败不影响主数据，返回 0 即可
		total = 0
	}

	s.writeJSON(w, map[string]interface{}{
		"items":  instruments,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

// handleSearchInstruments 处理股票搜索请求
func (s *Server) handleSearchInstruments(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	keyword := r.URL.Query().Get("q")
	limitStr := r.URL.Query().Get("limit")

	limit := 20
	if limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 && l <= 100 {
			limit = l
		}
	}

	instruments, err := s.stockV2.SearchInstruments(ctx, keyword, limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.writeJSON(w, map[string]interface{}{
		"items": instruments,
		"total": len(instruments),
	})
}

// handleGetInstrumentsByMarket 处理按市场获取股票请求
func (s *Server) handleGetInstrumentsByMarket(w http.ResponseWriter, r *http.Request) {
	market := r.PathValue("market")
	if market == "" {
		http.Error(w, "market is required", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	instruments, err := s.stockV2.GetInstrumentsByMarket(ctx, market)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.writeJSON(w, instruments)
}

// handleTriggerUpdate 处理触发更新请求
func (s *Server) handleTriggerUpdate(w http.ResponseWriter, r *http.Request) {
	var req stockv2.UniverseUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	// 设置默认值
	if req.TriggerType == "" {
		req.TriggerType = "manual"
	}
	if req.TriggerSource == "" {
		req.TriggerSource = "user"
	}

	ctx := r.Context()
	job, err := s.stockV2.ExecuteUniverseUpdate(ctx, req.TriggerType, req.TriggerSource)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	response := stockv2.UniverseUpdateResponse{
		JobID:   job.ID,
		Message: "Update job created successfully",
	}

	s.writeJSON(w, response)
}

// handleGetUpdateStatus 处理获取更新状态请求
func (s *Server) handleGetUpdateStatus(w http.ResponseWriter, r *http.Request) {
	jobId := r.PathValue("jobId")
	if jobId == "" {
		http.Error(w, "job ID is required", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	progress, err := s.stockV2.GetUpdateProgress(ctx, jobId)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	s.writeJSON(w, progress)
}

// handleGetLatestUpdate 处理获取最新更新请求
func (s *Server) handleGetLatestUpdate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	job, err := s.stockV2.GetLatestUpdateJob(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	response := map[string]interface{}{
		"job":      job,
		"progress": nil,
	}

	s.writeJSON(w, response)
}

// handleGetUpdateHistory 处理获取更新历史请求
func (s *Server) handleGetUpdateHistory(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// 解析查询参数
	limitStr := r.URL.Query().Get("limit")
	limit := 10 // 默认值

	if limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 && l <= 100 {
			limit = l
		}
	}

	jobs, err := s.stockV2.ListUpdateJobs(ctx, limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.writeJSON(w, map[string]interface{}{
		"items": jobs,
		"limit": limit,
	})
}

// handleStockV2GetSettings 处理获取 V2 设置请求
func (s *Server) handleStockV2GetSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	settings, err := s.stockV2.GetSettings(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.writeJSON(w, settings)
}

// handleStockV2UpdateSettings 处理更新 V2 设置请求
func (s *Server) handleStockV2UpdateSettings(w http.ResponseWriter, r *http.Request) {
	var req stockv2.RequestCreateOrUpdateSettings
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	settings, err := s.stockV2.CreateOrUpdateSettings(ctx, req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	s.writeJSON(w, settings)
}

// writeJSON 辅助函数：写入JSON响应
func (s *Server) writeJSON(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")

	if err := encoder.Encode(data); err != nil {
		s.log.Error("write JSON failed", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}
}

// parsePagination 解析分页参数
func parsePagination(r *http.Request) (limit, offset int) {
	// 默认值
	limit = 100
	offset = 0

	return limit, offset
}
