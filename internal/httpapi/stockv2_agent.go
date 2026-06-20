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

// ============================ authorizations ============================

func (s *Server) handleStockV2ListAgentAuthorizations(w http.ResponseWriter, r *http.Request) {
	filter, err := stockV2AgentAuthorizationFilterFromRequest(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	items, err := s.stockV2.ListAgentAuthorizations(r.Context(), filter)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	total, err := s.stockV2.CountAgentAuthorizations(r.Context(), filter)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.writeJSON(w, map[string]any{"items": items, "total": total, "limit": filter.Limit, "offset": filter.Offset})
}

func (s *Server) handleStockV2GetAgentAuthorization(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "authorization ID is required", http.StatusBadRequest)
		return
	}
	result, err := s.stockV2.GetAgentAuthorization(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), stockV2AgentHTTPStatus(err))
		return
	}
	s.writeJSON(w, result)
}

func (s *Server) handleStockV2ApproveAgentAuthorization(w http.ResponseWriter, r *http.Request) {
	s.handleAgentAuthorizationDecision(w, r, true)
}

func (s *Server) handleStockV2DenyAgentAuthorization(w http.ResponseWriter, r *http.Request) {
	s.handleAgentAuthorizationDecision(w, r, false)
}

// handleAgentAuthorizationDecision 推进授权闸;body 可空(不带 decisionReason)。
func (s *Server) handleAgentAuthorizationDecision(w http.ResponseWriter, r *http.Request, approve bool) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "authorization ID is required", http.StatusBadRequest)
		return
	}
	var req stockv2.RequestAgentAuthorizationDecision
	if r.Body != nil && r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
	}
	var (
		result stockv2.AgentAuthorization
		err    error
	)
	if approve {
		result, err = s.stockV2.ApproveAgentAuthorization(r.Context(), id, req)
	} else {
		result, err = s.stockV2.DenyAgentAuthorization(r.Context(), id, req)
	}
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
	limit, err = stockV2WatchPositiveInt(query.Get("limit"), 50)
	if err != nil || limit > 200 {
		return 0, 0, errors.New("invalid limit")
	}
	offset, err = stockV2WatchNonNegativeInt(query.Get("offset"), 0)
	if err != nil {
		return 0, 0, errors.New("invalid offset")
	}
	return limit, offset, nil
}

// stockV2HTTPValidAgentEnum 空值通过(不过滤),非空须命中白名单。
func stockV2HTTPValidAgentEnum(value string, valid ...string) bool {
	if value == "" {
		return true
	}
	for _, v := range valid {
		if v == value {
			return true
		}
	}
	return false
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

func stockV2AgentAuthorizationFilterFromRequest(r *http.Request) (stockv2.AgentAuthorizationListFilter, error) {
	query := r.URL.Query()
	limit, offset, err := stockV2AgentPage(r)
	if err != nil {
		return stockv2.AgentAuthorizationListFilter{}, err
	}
	status := query.Get("status")
	if !stockV2HTTPValidAgentEnum(status, stockv2.AgentAuthorizationStatusPending, stockv2.AgentAuthorizationStatusApproved, stockv2.AgentAuthorizationStatusDenied) {
		return stockv2.AgentAuthorizationListFilter{}, errors.New("invalid authorization status")
	}
	return stockv2.AgentAuthorizationListFilter{
		TaskType:          query.Get("taskType"),
		Status:            status,
		TriggerObjectType: query.Get("triggerObjectType"),
		TriggerObjectID:   query.Get("triggerObjectId"),
		Limit:             limit,
		Offset:            offset,
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
		errors.Is(err, stockv2.ErrAgentAuthorizationNotFound),
		errors.Is(err, stockv2.ErrAgentRunNotFound),
		errors.Is(err, stockv2.ErrAgentDecisionLedgerNotFound):
		return http.StatusNotFound
	case errors.Is(err, stockv2.ErrAgentAuthorizationAlreadyDecided),
		errors.Is(err, stockv2.ErrAgentModelNotAvailable):
		return http.StatusConflict
	case errors.Is(err, stockv2.ErrInvalidAgentProviderType),
		errors.Is(err, stockv2.ErrInvalidAgentProviderConfigState),
		errors.Is(err, stockv2.ErrInvalidAgentProviderAuthState),
		errors.Is(err, stockv2.ErrInvalidAgentProviderAvailability),
		errors.Is(err, stockv2.ErrInvalidAgentProviderName),
		errors.Is(err, stockv2.ErrInvalidAgentModelStatus),
		errors.Is(err, stockv2.ErrInvalidAgentModelCostLevel),
		errors.Is(err, stockv2.ErrInvalidAgentModelName),
		errors.Is(err, stockv2.ErrInvalidAgentTaskType):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}
