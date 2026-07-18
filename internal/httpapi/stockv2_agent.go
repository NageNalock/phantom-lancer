package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"phantom-lancer/internal/stockv2"
)

// Agent 治理层 HTTP。遵循 stockv2 风格:列表 {items,total,limit,offset},
// 单对象直接 struct,错误经 stockV2AgentHTTPStatus 映射,无 auth/CSRF。
// handler 不直接构造敏感字段,脱敏单点在 service;本轮不暴露真实模型调用。

// ============================ providers ============================

func (s *Server) handleStockV2ListAgentProviders(w http.ResponseWriter, r *http.Request) {
	filter, err := stockV2AgentProviderFilterFromRequest(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	items, err := s.stockV2.ListAgentProviderProfiles(r.Context(), filter)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	total, err := s.stockV2.CountAgentProviderProfiles(r.Context(), filter)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.writeJSON(w, map[string]any{"items": items, "total": total, "limit": filter.Limit, "offset": filter.Offset})
}

func (s *Server) handleStockV2CreateAgentProvider(w http.ResponseWriter, r *http.Request) {
	var req stockv2.RequestCreateAgentProviderProfile
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	result, err := s.stockV2.CreateAgentProviderProfile(r.Context(), req)
	if err != nil {
		http.Error(w, err.Error(), stockV2AgentHTTPStatus(err))
		return
	}
	s.writeJSON(w, result)
}

func (s *Server) handleStockV2GetAgentProvider(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "provider ID is required", http.StatusBadRequest)
		return
	}
	result, err := s.stockV2.GetAgentProviderProfile(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), stockV2AgentHTTPStatus(err))
		return
	}
	s.writeJSON(w, result)
}

func (s *Server) handleStockV2UpdateAgentProvider(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "provider ID is required", http.StatusBadRequest)
		return
	}
	var req stockv2.RequestUpdateAgentProviderProfile
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	result, err := s.stockV2.UpdateAgentProviderProfile(r.Context(), id, req)
	if err != nil {
		http.Error(w, err.Error(), stockV2AgentHTTPStatus(err))
		return
	}
	s.writeJSON(w, result)
}

func (s *Server) handleStockV2DeleteAgentProvider(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "provider ID is required", http.StatusBadRequest)
		return
	}
	if err := s.stockV2.DeleteAgentProviderProfile(r.Context(), id); err != nil {
		http.Error(w, err.Error(), stockV2AgentHTTPStatus(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleStockV2ListAgentProviderModels(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "provider ID is required", http.StatusBadRequest)
		return
	}
	result, err := s.stockV2.ListAgentProviderModels(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), stockV2AgentHTTPStatus(err))
		return
	}
	s.writeJSON(w, result)
}

// ============================ models ============================

func (s *Server) handleStockV2ListAgentModels(w http.ResponseWriter, r *http.Request) {
	filter, err := stockV2AgentModelFilterFromRequest(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	items, err := s.stockV2.ListAgentModelProfiles(r.Context(), filter)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	total, err := s.stockV2.CountAgentModelProfiles(r.Context(), filter)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.writeJSON(w, map[string]any{"items": items, "total": total, "limit": filter.Limit, "offset": filter.Offset})
}

func (s *Server) handleStockV2CreateAgentModel(w http.ResponseWriter, r *http.Request) {
	var req stockv2.RequestCreateAgentModelProfile
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	result, err := s.stockV2.CreateAgentModelProfile(r.Context(), req)
	if err != nil {
		http.Error(w, err.Error(), stockV2AgentHTTPStatus(err))
		return
	}
	s.writeJSON(w, result)
}

func (s *Server) handleStockV2GetAgentModel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "model ID is required", http.StatusBadRequest)
		return
	}
	result, err := s.stockV2.GetAgentModelProfile(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), stockV2AgentHTTPStatus(err))
		return
	}
	s.writeJSON(w, result)
}

func (s *Server) handleStockV2UpdateAgentModel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "model ID is required", http.StatusBadRequest)
		return
	}
	var req stockv2.RequestUpdateAgentModelProfile
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	result, err := s.stockV2.UpdateAgentModelProfile(r.Context(), id, req)
	if err != nil {
		http.Error(w, err.Error(), stockV2AgentHTTPStatus(err))
		return
	}
	s.writeJSON(w, result)
}

func (s *Server) handleStockV2TestAgentModel(w http.ResponseWriter, r *http.Request) {
	var req stockv2.RequestTestAgentModel
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	result, err := s.stockV2.TestAgentModel(r.Context(), req)
	if err != nil {
		http.Error(w, err.Error(), stockV2AgentHTTPStatus(err))
		return
	}
	s.writeJSON(w, result)
}

// ============================ task profiles ============================

func (s *Server) handleStockV2ListAgentTaskProfiles(w http.ResponseWriter, r *http.Request) {
	filter, err := stockV2AgentTaskProfileFilterFromRequest(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	items, err := s.stockV2.ListAgentTaskProfiles(r.Context(), filter)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	total, err := s.stockV2.CountAgentTaskProfiles(r.Context(), filter)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.writeJSON(w, map[string]any{"items": items, "total": total, "limit": filter.Limit, "offset": filter.Offset})
}

func (s *Server) handleStockV2GetAgentTaskProfile(w http.ResponseWriter, r *http.Request) {
	taskType := r.PathValue("taskType")
	if taskType == "" {
		http.Error(w, "task type is required", http.StatusBadRequest)
		return
	}
	result, err := s.stockV2.GetAgentTaskProfileByType(r.Context(), taskType)
	if err != nil {
		http.Error(w, err.Error(), stockV2AgentHTTPStatus(err))
		return
	}
	s.writeJSON(w, result)
}

func (s *Server) handleStockV2UpdateAgentTaskProfile(w http.ResponseWriter, r *http.Request) {
	taskType := r.PathValue("taskType")
	if taskType == "" {
		http.Error(w, "task type is required", http.StatusBadRequest)
		return
	}
	var req stockv2.RequestUpdateAgentTaskProfile
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	result, err := s.stockV2.UpdateAgentTaskProfile(r.Context(), taskType, req)
	if err != nil {
		http.Error(w, err.Error(), stockV2AgentHTTPStatus(err))
		return
	}
	s.writeJSON(w, result)
}

// ============================ runs + ledgers ============================

func (s *Server) handleStockV2ListAgentRuns(w http.ResponseWriter, r *http.Request) {
	filter, err := stockV2AgentRunFilterFromRequest(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	items, err := s.stockV2.ListAgentRuns(r.Context(), filter)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	total, err := s.stockV2.CountAgentRuns(r.Context(), filter)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.writeJSON(w, map[string]any{"items": items, "total": total, "limit": filter.Limit, "offset": filter.Offset})
}

func (s *Server) handleStockV2GetAgentRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "run ID is required", http.StatusBadRequest)
		return
	}
	result, err := s.stockV2.GetAgentRun(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), stockV2AgentHTTPStatus(err))
		return
	}
	s.writeJSON(w, result)
}

func (s *Server) handleStockV2GetAgentRunDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "run ID is required", http.StatusBadRequest)
		return
	}
	result, err := s.stockV2.GetAgentExecutionDetail(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), stockV2AgentHTTPStatus(err))
		return
	}
	s.writeJSON(w, result)
}

func (s *Server) handleStockV2GetAgentDecisionLedger(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "ledger ID is required", http.StatusBadRequest)
		return
	}
	result, err := s.stockV2.GetAgentDecisionLedger(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), stockV2AgentHTTPStatus(err))
		return
	}
	s.writeJSON(w, result)
}

func (s *Server) handleStockV2RunAgentCLIDebug(w http.ResponseWriter, r *http.Request) {
	var req stockv2.RequestRunAgentCLIDebug
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	result, err := s.stockV2.RunAgentCLIDebug(r.Context(), req)
	if err != nil {
		http.Error(w, err.Error(), stockV2AgentHTTPStatus(err))
		return
	}
	s.writeJSON(w, result)
}

func (s *Server) handleStockV2RunStrategyGeneration(w http.ResponseWriter, r *http.Request) {
	var req stockv2.StrategyGenerationInput
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	result, err := s.stockV2.RunStrategyGeneration(r.Context(), req)
	if err != nil {
		http.Error(w, err.Error(), stockV2AgentHTTPStatus(err))
		return
	}
	s.writeJSON(w, result)
}

// ============================ resolve ============================

func (s *Server) handleStockV2ResolveAgentTask(w http.ResponseWriter, r *http.Request) {
	var req stockv2.RequestResolveAgentTask
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	result, err := s.stockV2.ResolveAgentTask(r.Context(), req.TaskType, req.TriggerObjectType, req.TriggerObjectID, req.RequestedBy)
	if err != nil {
		http.Error(w, err.Error(), stockV2AgentHTTPStatus(err))
		return
	}
	s.writeJSON(w, result)
}

// ============================ filter / helpers ============================

func stockV2AgentPage(r *http.Request) (limit, offset int, err error) {
	query := r.URL.Query()
	limit, err = stockV2PositiveInt(query.Get("limit"), 50)
	if err != nil || limit > 200 {
		return 0, 0, errors.New("invalid limit")
	}
	offset, err = stockV2NonNegativeInt(query.Get("offset"), 0)
	if err != nil {
		return 0, 0, errors.New("invalid offset")
	}
	return limit, offset, nil
}

// stockV2HTTPValidAgentEnum 空值通过(不过滤),非空须命中白名单。
func stockV2HTTPValidAgentEnum(value string, valid ...string) bool {
	return value == "" || stockV2HTTPValueIn(value, valid...)
}

func stockV2AgentProviderFilterFromRequest(r *http.Request) (stockv2.AgentProviderProfileListFilter, error) {
	query := r.URL.Query()
	limit, offset, err := stockV2AgentPage(r)
	if err != nil {
		return stockv2.AgentProviderProfileListFilter{}, err
	}
	providerType := query.Get("providerType")
	if !stockV2HTTPValidAgentEnum(providerType, stockv2.AgentProviderTypeOpenAI, stockv2.AgentProviderTypeCodexCLI, stockv2.AgentProviderTypeLocal) {
		return stockv2.AgentProviderProfileListFilter{}, errors.New("invalid provider type")
	}
	configState := query.Get("configState")
	if !stockV2HTTPValidAgentEnum(configState, stockv2.AgentProviderConfigStateNotConfigured, stockv2.AgentProviderConfigStateConfigured, stockv2.AgentProviderConfigStateMisconfigured) {
		return stockv2.AgentProviderProfileListFilter{}, errors.New("invalid provider config state")
	}
	authState := query.Get("authState")
	if !stockV2HTTPValidAgentEnum(authState, stockv2.AgentProviderAuthStateUnauthenticated, stockv2.AgentProviderAuthStateAuthenticated, stockv2.AgentProviderAuthStateExpired, stockv2.AgentProviderAuthStateUnknown) {
		return stockv2.AgentProviderProfileListFilter{}, errors.New("invalid provider auth state")
	}
	availability := query.Get("availability")
	if !stockV2HTTPValidAgentEnum(availability, stockv2.AgentProviderAvailabilityUnknown, stockv2.AgentProviderAvailabilityAvailable, stockv2.AgentProviderAvailabilityUnavailable, stockv2.AgentProviderAvailabilityDegraded) {
		return stockv2.AgentProviderProfileListFilter{}, errors.New("invalid provider availability")
	}
	return stockv2.AgentProviderProfileListFilter{
		ProviderType: providerType,
		ConfigState:  configState,
		AuthState:    authState,
		Availability: availability,
		Limit:        limit,
		Offset:       offset,
	}, nil
}

func stockV2AgentModelFilterFromRequest(r *http.Request) (stockv2.AgentModelProfileListFilter, error) {
	query := r.URL.Query()
	limit, offset, err := stockV2AgentPage(r)
	if err != nil {
		return stockv2.AgentModelProfileListFilter{}, err
	}
	status := query.Get("status")
	if !stockV2HTTPValidAgentEnum(status, stockv2.AgentModelStatusAvailable, stockv2.AgentModelStatusDegraded, stockv2.AgentModelStatusUnavailable) {
		return stockv2.AgentModelProfileListFilter{}, errors.New("invalid model status")
	}
	costLevel := query.Get("costLevel")
	if !stockV2HTTPValidAgentEnum(costLevel, stockv2.AgentModelCostLevelLow, stockv2.AgentModelCostLevelMedium, stockv2.AgentModelCostLevelHigh) {
		return stockv2.AgentModelProfileListFilter{}, errors.New("invalid model cost level")
	}
	var enabled *bool
	switch strings.ToLower(query.Get("enabled")) {
	case "true":
		v := true
		enabled = &v
	case "false":
		v := false
		enabled = &v
	}
	return stockv2.AgentModelProfileListFilter{
		ProviderID: query.Get("providerId"),
		Status:     status,
		CostLevel:  costLevel,
		Enabled:    enabled,
		Limit:      limit,
		Offset:     offset,
	}, nil
}

func stockV2AgentTaskProfileFilterFromRequest(r *http.Request) (stockv2.AgentTaskProfileListFilter, error) {
	limit, offset, err := stockV2AgentPage(r)
	if err != nil {
		return stockv2.AgentTaskProfileListFilter{}, err
	}
	return stockv2.AgentTaskProfileListFilter{
		TaskType: r.URL.Query().Get("taskType"),
		Limit:    limit,
		Offset:   offset,
	}, nil
}

func stockV2AgentRunFilterFromRequest(r *http.Request) (stockv2.AgentRunListFilter, error) {
	query := r.URL.Query()
	limit, offset, err := stockV2AgentPage(r)
	if err != nil {
		return stockv2.AgentRunListFilter{}, err
	}
	status := query.Get("status")
	if !stockV2HTTPValidAgentEnum(status, stockv2.AgentRunStatusPending, stockv2.AgentRunStatusReady, stockv2.AgentRunStatusRunning, stockv2.AgentRunStatusCompleted, stockv2.AgentRunStatusFailed) {
		return stockv2.AgentRunListFilter{}, errors.New("invalid run status")
	}
	return stockv2.AgentRunListFilter{
		TaskType:          query.Get("taskType"),
		Status:            status,
		ProviderID:        query.Get("providerId"),
		ModelID:           query.Get("modelId"),
		TriggerObjectType: query.Get("triggerObjectType"),
		TriggerObjectID:   query.Get("triggerObjectId"),
		Limit:             limit,
		Offset:            offset,
	}, nil
}

func stockV2AgentHTTPStatus(err error) int {
	switch {
	case errors.Is(err, stockv2.ErrAgentProviderNotFound),
		errors.Is(err, stockv2.ErrAgentModelNotFound),
		errors.Is(err, stockv2.ErrAgentTaskProfileNotFound),
		errors.Is(err, stockv2.ErrAgentRunNotFound),
		errors.Is(err, stockv2.ErrAgentDecisionLedgerNotFound):
		return http.StatusNotFound
	case errors.Is(err, stockv2.ErrAgentModelNotAvailable),
		errors.Is(err, stockv2.ErrAgentExecutorUnavailable),
		errors.Is(err, stockv2.ErrAgentTaskNotConfigurable),
		errors.Is(err, stockv2.ErrAgentProviderProtected),
		errors.Is(err, stockv2.ErrEmbeddingModelNotConfigured),
		errors.Is(err, stockv2.ErrEmbeddingModelUnavailable),
		errors.Is(err, stockv2.ErrEmbeddingAssetNotReady):
		return http.StatusConflict
	case errors.Is(err, stockv2.ErrInvalidAgentProviderType),
		errors.Is(err, stockv2.ErrInvalidAgentProviderConfigState),
		errors.Is(err, stockv2.ErrInvalidAgentProviderAuthState),
		errors.Is(err, stockv2.ErrInvalidAgentProviderAvailability),
		errors.Is(err, stockv2.ErrInvalidAgentProviderName),
		errors.Is(err, stockv2.ErrAgentProviderAPIKeyRequired),
		errors.Is(err, stockv2.ErrAgentProviderBaseURLRequired),
		errors.Is(err, stockv2.ErrInvalidAgentModelStatus),
		errors.Is(err, stockv2.ErrInvalidAgentModelCostLevel),
		errors.Is(err, stockv2.ErrInvalidAgentModelType),
		errors.Is(err, stockv2.ErrInvalidAgentModelName),
		errors.Is(err, stockv2.ErrInvalidAgentReasoningEffort),
		errors.Is(err, stockv2.ErrAgentModelTypeNotAllowed),
		errors.Is(err, stockv2.ErrInvalidAgentTaskType),
		errors.Is(err, stockv2.ErrInvalidEmbeddingConfig),
		errors.Is(err, stockv2.ErrInvalidEmbeddingRequest),
		errors.Is(err, stockv2.ErrInvalidStrategyGenerationInput),
		errors.Is(err, stockv2.ErrInvalidStrategyGenerationResult):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

// handleStockV2AgentMCPStatus 返回给 UI 的 Codex MCP loopback 状态。
func (s *Server) handleStockV2AgentMCPStatus(w http.ResponseWriter, r *http.Request) {
	s.writeJSON(w, s.stockV2.AgentMCPStatus())
}
