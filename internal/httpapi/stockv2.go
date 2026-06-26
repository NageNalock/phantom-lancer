package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

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
	mux.HandleFunc("POST /api/stockv2/portfolios/{id}/refresh", s.handleStockV2RefreshPortfolioValuation)
	mux.HandleFunc("GET /api/stockv2/portfolios/{id}/snapshots", s.handleStockV2GetPortfolioSnapshots)
	mux.HandleFunc("POST /api/stockv2/portfolios/{id}/holdings", s.handleCreateHolding)
	mux.HandleFunc("GET /api/stockv2/portfolios/{id}/holdings", s.handleListHoldings)
	mux.HandleFunc("PUT /api/stockv2/portfolios/{id}/holdings/{holdingId}", s.handleUpdateHolding)
	mux.HandleFunc("DELETE /api/stockv2/portfolios/{id}/holdings/{holdingId}", s.handleDeleteHolding)
	mux.HandleFunc("POST /api/stockv2/portfolios/{id}/transactions", s.handleStockV2RecordTransaction)
	mux.HandleFunc("GET /api/stockv2/portfolios/{id}/transactions", s.handleStockV2ListTransactions)
	mux.HandleFunc("GET /api/stockv2/portfolios/{id}/asset-curve", s.handleStockV2GetAssetCurve)

	// 股票主数据
	mux.HandleFunc("GET /api/stockv2/instruments", s.handleListInstruments)
	mux.HandleFunc("GET /api/stockv2/instruments/market/{market}", s.handleGetInstrumentsByMarket)
	mux.HandleFunc("GET /api/stockv2/instruments/search", s.handleSearchInstruments)
	mux.HandleFunc("GET /api/stockv2/profiles", s.handleStockV2ListStockProfiles)
	mux.HandleFunc("GET /api/stockv2/profiles/summaries", s.handleStockV2ListStockProfileSummaries)
	mux.HandleFunc("GET /api/stockv2/profiles/update-tasks", s.handleStockV2ListStockProfileUpdateTasks)
	mux.HandleFunc("GET /api/stockv2/profiles/{symbol}", s.handleStockV2GetStockProfile)
	mux.HandleFunc("POST /api/stockv2/profiles/{symbol}/update", s.handleStockV2UpdateStockProfile)
	mux.HandleFunc("GET /api/stockv2/profiles/{symbol}/update-tasks", s.handleStockV2ListStockProfileUpdateTasks)
	mux.HandleFunc("POST /api/stockv2/profiles/{symbol}/build", s.handleStockV2BuildStockProfile)
	mux.HandleFunc("POST /api/stockv2/profiles/{symbol}/run-agent", s.handleStockV2RunStockProfileAgent)
	mux.HandleFunc("POST /api/stockv2/profiles/rebuild", s.handleStockV2RebuildStockProfiles)

	// 最新行情
	mux.HandleFunc("GET /api/stockv2/quotes/latest", s.handleStockV2GetLatestQuotes)
	mux.HandleFunc("GET /api/stockv2/quotes/refresh-state", s.handleStockV2GetQuoteRefreshState)
	mux.HandleFunc("POST /api/stockv2/quotes/refresh", s.handleStockV2RefreshLatestQuotes)
	mux.HandleFunc("GET /api/stockv2/intraday/minute-bars", s.handleStockV2ListMinuteBars)

	// 策略对象
	mux.HandleFunc("GET /api/stockv2/strategies", s.handleStockV2ListStrategies)
	mux.HandleFunc("POST /api/stockv2/strategies", s.handleStockV2CreateStrategy)
	mux.HandleFunc("GET /api/stockv2/strategies/{id}", s.handleStockV2GetStrategy)
	mux.HandleFunc("PUT /api/stockv2/strategies/{id}", s.handleStockV2UpdateStrategy)
	mux.HandleFunc("POST /api/stockv2/strategies/{id}/activate", s.handleStockV2ActivateStrategy)
	mux.HandleFunc("POST /api/stockv2/strategies/{id}/pause", s.handleStockV2PauseStrategy)
	mux.HandleFunc("POST /api/stockv2/strategies/{id}/archive", s.handleStockV2ArchiveStrategy)
	mux.HandleFunc("GET /api/stockv2/strategies/{id}/versions", s.handleStockV2ListStrategyVersions)
	mux.HandleFunc("POST /api/stockv2/portfolios/{id}/monitor-strategy", s.handleStockV2CreatePortfolioMonitorStrategy)
	mux.HandleFunc("POST /api/stockv2/strategies/{id}/create-watch", s.handleStockV2CreateStrategyWatch)
	mux.HandleFunc("POST /api/stockv2/portfolios/{id}/create-monitor-watch", s.handleStockV2CreatePortfolioMonitorWatch)

	// 盯盘与提醒
	mux.HandleFunc("GET /api/stockv2/watches", s.handleStockV2ListWatches)
	mux.HandleFunc("POST /api/stockv2/watches", s.handleStockV2CreateWatch)
	mux.HandleFunc("GET /api/stockv2/watches/{id}", s.handleStockV2GetWatch)
	mux.HandleFunc("PUT /api/stockv2/watches/{id}", s.handleStockV2UpdateWatch)
	mux.HandleFunc("POST /api/stockv2/watches/{id}/run", s.handleStockV2RunWatch)
	mux.HandleFunc("POST /api/stockv2/watches/{id}/activate", s.handleStockV2ActivateWatch)
	mux.HandleFunc("POST /api/stockv2/watches/{id}/pause", s.handleStockV2PauseWatch)
	mux.HandleFunc("POST /api/stockv2/watches/{id}/archive", s.handleStockV2ArchiveWatch)
	mux.HandleFunc("GET /api/stockv2/alerts", s.handleStockV2ListAlerts)
	mux.HandleFunc("POST /api/stockv2/alerts/{id}/ack", s.handleStockV2AckAlert)
	mux.HandleFunc("POST /api/stockv2/alerts/{id}/ignore", s.handleStockV2IgnoreAlert)
	mux.HandleFunc("POST /api/stockv2/alerts/{id}/resolve", s.handleStockV2ResolveAlert)

	// 监控与任务(系统固化后台监控的可观测性;不暴露「新建盯盘」)
	mux.HandleFunc("GET /api/stockv2/monitor/tasks", s.handleStockV2ListMonitorTasks)
	mux.HandleFunc("PUT /api/stockv2/monitor/tasks/{taskType}/config", s.handleStockV2UpdateMonitorTaskConfig)
	mux.HandleFunc("POST /api/stockv2/monitor/tasks/{taskType}/run", s.handleStockV2RunMonitorTask)
	mux.HandleFunc("GET /api/stockv2/monitor/runs", s.handleStockV2ListMonitorRuns)
	mux.HandleFunc("GET /api/stockv2/monitor/runs/{id}/agent-runs", s.handleStockV2ListMonitorRunAgentRuns)
	mux.HandleFunc("GET /api/stockv2/monitor/runs/{id}", s.handleStockV2GetMonitorRun)
	mux.HandleFunc("GET /api/stockv2/monitor/hits", s.handleStockV2ListMonitorHits)
	mux.HandleFunc("GET /api/stockv2/monitor/hits/{id}", s.handleStockV2GetMonitorHit)
	mux.HandleFunc("POST /api/stockv2/monitor/hits/{id}/review", s.handleStockV2CreateReviewFromMonitorHit)

	// Operation Review(从 MonitorHit 进入人工/后续 Agent 审阅)
	mux.HandleFunc("GET /api/stockv2/reviews", s.handleStockV2ListOperationReviews)
	mux.HandleFunc("GET /api/stockv2/reviews/{id}", s.handleStockV2GetOperationReview)
	mux.HandleFunc("PUT /api/stockv2/reviews/{id}/result", s.handleStockV2SaveOperationReviewResult)
	mux.HandleFunc("POST /api/stockv2/reviews/{id}/accept", s.handleStockV2AcceptOperationReview)
	mux.HandleFunc("POST /api/stockv2/reviews/{id}/reject", s.handleStockV2RejectOperationReview)
	mux.HandleFunc("POST /api/stockv2/reviews/{id}/defer", s.handleStockV2DeferOperationReview)
	mux.HandleFunc("POST /api/stockv2/reviews/{id}/run-agent", s.handleStockV2RunAgentReview)

	// 更新任务
	mux.HandleFunc("POST /api/stockv2/update/trigger", s.handleTriggerUpdate)
	mux.HandleFunc("GET /api/stockv2/update/status/{jobId}", s.handleGetUpdateStatus)
	mux.HandleFunc("GET /api/stockv2/update/latest", s.handleGetLatestUpdate)
	mux.HandleFunc("GET /api/stockv2/update/history", s.handleGetUpdateHistory)

	// 配置管理
	mux.HandleFunc("GET /api/stockv2/settings", s.handleStockV2GetSettings)
	mux.HandleFunc("PUT /api/stockv2/settings", s.handleStockV2UpdateSettings)

	// 消息源 adapter：只落 RawNews，不触发 Agent / Review。
	mux.HandleFunc("GET /api/stockv2/news/sources", s.handleStockV2ListNewsSources)
	mux.HandleFunc("PUT /api/stockv2/news/sources/{source}/config", s.handleStockV2UpdateNewsSourceConfig)
	mux.HandleFunc("POST /api/stockv2/news/sources/{source}/run-once", s.handleStockV2RunNewsSourceOnce)
	mux.HandleFunc("POST /api/stockv2/news/sources/{source}/fetch", s.handleStockV2FetchRawNewsSource)
	mux.HandleFunc("GET /api/stockv2/news/raw", s.handleStockV2ListRawNews)
	mux.HandleFunc("POST /api/stockv2/news/raw/truncate", s.handleStockV2TruncateRawNews)
	mux.HandleFunc("GET /api/stockv2/news/raw/{id}", s.handleStockV2GetRawNews)
	mux.HandleFunc("GET /api/stockv2/news/events", s.handleStockV2ListNewsEvents)
	mux.HandleFunc("GET /api/stockv2/news/link-candidates", s.handleStockV2ListNewsLinkCandidates)

	// 日级历史行情（Daily Bars）
	mux.HandleFunc("POST /api/stockv2/history/daily/ensure", s.handleEnsureDailyBars)
	mux.HandleFunc("GET /api/stockv2/history/daily", s.handleGetDailyBars)
	mux.HandleFunc("GET /api/stockv2/history/daily/qualities", s.handleGetDailyBarsQualities)
	mux.HandleFunc("GET /api/stockv2/history/daily/quality", s.handleGetDailyBarsQuality)
	mux.HandleFunc("POST /api/stockv2/history/daily/jobs/run", s.handleRunDailyBarsJob)
	mux.HandleFunc("GET /api/stockv2/history/daily/jobs/{jobId}", s.handleGetDailyBarsJob)
	mux.HandleFunc("GET /api/stockv2/history/daily/jobs", s.handleListDailyBarsJobs)

	// Agent 治理层(provider/model/task profile、运行与决策留痕)
	mux.HandleFunc("GET /api/stockv2/agent/providers", s.handleStockV2ListAgentProviders)
	mux.HandleFunc("POST /api/stockv2/agent/providers", s.handleStockV2CreateAgentProvider)
	mux.HandleFunc("GET /api/stockv2/agent/providers/{id}", s.handleStockV2GetAgentProvider)
	mux.HandleFunc("PUT /api/stockv2/agent/providers/{id}", s.handleStockV2UpdateAgentProvider)
	mux.HandleFunc("DELETE /api/stockv2/agent/providers/{id}", s.handleStockV2DeleteAgentProvider)
	mux.HandleFunc("GET /api/stockv2/agent/providers/{id}/models", s.handleStockV2ListAgentProviderModels)
	mux.HandleFunc("GET /api/stockv2/agent/models", s.handleStockV2ListAgentModels)
	mux.HandleFunc("POST /api/stockv2/agent/models", s.handleStockV2CreateAgentModel)
	mux.HandleFunc("POST /api/stockv2/agent/models/test", s.handleStockV2TestAgentModel)
	mux.HandleFunc("GET /api/stockv2/agent/models/{id}", s.handleStockV2GetAgentModel)
	mux.HandleFunc("PUT /api/stockv2/agent/models/{id}", s.handleStockV2UpdateAgentModel)
	mux.HandleFunc("GET /api/stockv2/agent/task-profiles", s.handleStockV2ListAgentTaskProfiles)
	mux.HandleFunc("GET /api/stockv2/agent/task-profiles/{taskType}", s.handleStockV2GetAgentTaskProfile)
	mux.HandleFunc("PUT /api/stockv2/agent/task-profiles/{taskType}", s.handleStockV2UpdateAgentTaskProfile)
	mux.HandleFunc("POST /api/stockv2/agent/cli-debug", s.handleStockV2RunAgentCLIDebug)
	mux.HandleFunc("POST /api/stockv2/agent/strategy-generation/run", s.handleStockV2RunStrategyGeneration)
	mux.HandleFunc("GET /api/stockv2/agent/runs", s.handleStockV2ListAgentRuns)
	mux.HandleFunc("GET /api/stockv2/agent/runs/{id}/detail", s.handleStockV2GetAgentRunDetail)
	mux.HandleFunc("GET /api/stockv2/agent/runs/{id}", s.handleStockV2GetAgentRun)
	mux.HandleFunc("GET /api/stockv2/agent/ledgers/{id}", s.handleStockV2GetAgentDecisionLedger)
	mux.HandleFunc("POST /api/stockv2/agent/resolve", s.handleStockV2ResolveAgentTask)
	mux.HandleFunc("GET /api/stockv2/agent/mcp/status", s.handleStockV2AgentMCPStatus)
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
	market := r.URL.Query().Get("market")
	instrumentType := r.URL.Query().Get("instrumentType")

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

	instruments, err := s.stockV2.GetInstrumentsFiltered(ctx, market, instrumentType, limit, offset)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	total, err := s.stockV2.CountInstrumentsFiltered(ctx, market, instrumentType)
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
	market := r.URL.Query().Get("market")
	instrumentType := r.URL.Query().Get("instrumentType")
	limitStr := r.URL.Query().Get("limit")

	limit := 20
	if limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 && l <= 100 {
			limit = l
		}
	}

	instruments, err := s.stockV2.SearchInstrumentsFiltered(ctx, keyword, market, instrumentType, limit)
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
	session, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, session.Session) {
		return
	}
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

func (s *Server) handleStockV2FetchRawNewsSource(w http.ResponseWriter, r *http.Request) {
	session, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, session.Session) {
		return
	}
	result, err := s.stockV2.FetchRawNewsFromSource(r.Context(), r.PathValue("source"))
	if err != nil {
		http.Error(w, err.Error(), stockV2NewsHTTPStatus(err))
		return
	}
	s.writeJSON(w, result)
}

// Daily Bars.

// handleEnsureDailyBars 按需补拉单只股票的日 K。
// 若本地已覆盖且未过时则直接跳过；否则异步启动抓取并返回 job id 供前端轮询。
func (s *Server) handleEnsureDailyBars(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Symbol   string `json:"symbol"`
		Range    string `json:"range"`    // 6m | 1y | 3y | 5y
		Adjusted string `json:"adjusted"` // none | qfq | hfq
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Symbol == "" {
		http.Error(w, "symbol is required", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	result, err := s.stockV2.EnsureDailyBars(ctx, req.Symbol, req.Range, req.Adjusted)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.writeJSON(w, result)
}

// handleGetDailyBars 查询本地日 K（不触发抓取）。
func (s *Server) handleGetDailyBars(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	symbol := q.Get("symbol")
	if symbol == "" {
		http.Error(w, "symbol is required", http.StatusBadRequest)
		return
	}
	adjusted := q.Get("adjusted")
	start := q.Get("start")
	end := q.Get("end")
	limit := 0
	if ls := q.Get("limit"); ls != "" {
		if l, err := strconv.Atoi(ls); err == nil && l > 0 {
			limit = l
		}
	}

	ctx := r.Context()
	bars, err := s.stockV2.GetDailyBars(ctx, symbol, limit, start, end, adjusted)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.writeJSON(w, map[string]interface{}{
		"items": bars,
		"total": len(bars),
		"limit": limit,
	})
}

// handleGetDailyBarsQuality 评估本地日 K 数据质量（行数、区间、陈旧度等）。
func (s *Server) handleGetDailyBarsQuality(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	symbol := q.Get("symbol")
	if symbol == "" {
		http.Error(w, "symbol is required", http.StatusBadRequest)
		return
	}
	adjusted := q.Get("adjusted")

	ctx := r.Context()
	quality, err := s.stockV2.GetDailyBarsQuality(ctx, symbol, adjusted)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.writeJSON(w, quality)
}

func (s *Server) handleGetDailyBarsQualities(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	rawSymbols := strings.TrimSpace(q.Get("symbols"))
	if rawSymbols == "" {
		http.Error(w, "symbols is required", http.StatusBadRequest)
		return
	}
	adjusted := q.Get("adjusted")

	qualities, err := s.stockV2.GetDailyBarsQualityBatch(r.Context(), strings.Split(rawSymbols, ","), adjusted)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.writeJSON(w, map[string]any{"items": qualities})
}

// handleRunDailyBarsJob 手动触发日 K 批量任务（symbol / hot / universe_incremental）。
func (s *Server) handleRunDailyBarsJob(w http.ResponseWriter, r *http.Request) {
	var req stockv2.DailyBarsJobRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Mode == "" {
		req.Mode = stockv2.DailyBarJobModeHot
	}
	if req.TriggerType == "" {
		req.TriggerType = "manual"
	}
	if req.TriggerSource == "" {
		req.TriggerSource = "web"
	}

	ctx := r.Context()
	job, err := s.stockV2.RunDailyBarsJob(ctx, req)
	if err != nil {
		status := http.StatusInternalServerError
		if err == stockv2.ErrDailyBarJobAlreadyRunning {
			status = http.StatusConflict
		} else if err.Error() == "symbol is required for symbol mode" {
			status = http.StatusBadRequest
		}
		http.Error(w, err.Error(), status)
		return
	}
	s.writeJSON(w, job)
}

// handleGetDailyBarsJob 获取单个日 K 任务，供单票详情抽屉按 jobId 可靠轮询。
func (s *Server) handleGetDailyBarsJob(w http.ResponseWriter, r *http.Request) {
	jobID := r.PathValue("jobId")
	if jobID == "" {
		http.Error(w, "job ID is required", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	job, err := s.stockV2.GetDailyBarJob(ctx, jobID)
	if err != nil {
		status := http.StatusInternalServerError
		if err == stockv2.ErrDailyBarJobNotFound {
			status = http.StatusNotFound
		}
		http.Error(w, err.Error(), status)
		return
	}
	s.writeJSON(w, job)
}

// handleListDailyBarsJobs 列出最近的日 K 任务记录（供前端轮询进度）。
func (s *Server) handleListDailyBarsJobs(w http.ResponseWriter, r *http.Request) {
	limit := 20
	if ls := r.URL.Query().Get("limit"); ls != "" {
		if l, err := strconv.Atoi(ls); err == nil && l > 0 && l <= 100 {
			limit = l
		}
	}
	offset := 0
	if offsetText := r.URL.Query().Get("offset"); offsetText != "" {
		if o, err := strconv.Atoi(offsetText); err == nil && o > 0 {
			offset = o
		}
	}

	ctx := r.Context()
	jobs, err := s.stockV2.ListDailyBarJobs(ctx, limit, offset)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	total, err := s.stockV2.CountDailyBarJobs(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	running, err := s.stockV2.ListRunningDailyBarJobs(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.writeJSON(w, map[string]interface{}{
		"items":   jobs,
		"running": running,
		"total":   total,
		"limit":   limit,
		"offset":  offset,
	})
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
