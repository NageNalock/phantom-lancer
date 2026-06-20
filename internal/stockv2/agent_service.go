package stockv2

import (
	"context"
	"fmt"
	"strings"
	"time"

	"phantom-lancer/internal/safelog"
)

// Agent 治理层 service。脱敏单点在此层(经 internal/safelog);store 只持久化,
// HTTP handler 不直接构造敏感字段。本轮不真实调用外部模型,不写假 AI 结论;
// AgentRun 停在 ready,Output 留空,等待后续轮次接入真实 executor。

// ============================ provider profiles ============================

func (s *Service) ListAgentProviderProfiles(ctx context.Context, filter AgentProviderProfileListFilter) ([]AgentProviderProfile, error) {
	return s.store.ListAgentProviderProfiles(ctx, filter)
}

func (s *Service) GetAgentProviderProfile(ctx context.Context, id string) (AgentProviderProfile, error) {
	return s.store.GetAgentProviderProfile(ctx, id)
}

func (s *Service) CreateAgentProviderProfile(ctx context.Context, req RequestCreateAgentProviderProfile) (AgentProviderProfile, error) {
	if !validAgentProviderType(req.ProviderType) {
		return AgentProviderProfile{}, ErrInvalidAgentProviderType
	}
	if strings.TrimSpace(req.Name) == "" {
		return AgentProviderProfile{}, ErrInvalidAgentProviderName
	}
	configState := strings.TrimSpace(req.ConfigState)
	if configState == "" {
		configState = AgentProviderConfigStateNotConfigured
	} else if !validAgentProviderConfigState(configState) {
		return AgentProviderProfile{}, ErrInvalidAgentProviderConfigState
	}
	authState := strings.TrimSpace(req.AuthState)
	if authState == "" {
		authState = AgentProviderAuthStateUnknown
	} else if !validAgentProviderAuthState(authState) {
		return AgentProviderProfile{}, ErrInvalidAgentProviderAuthState
	}
	availability := strings.TrimSpace(req.Availability)
	if availability == "" {
		availability = AgentProviderAvailabilityUnknown
	} else if !validAgentProviderAvailability(availability) {
		return AgentProviderProfile{}, ErrInvalidAgentProviderAvailability
	}
	profile := AgentProviderProfile{
		ProviderType: req.ProviderType,
		Name:         strings.TrimSpace(req.Name),
		DisplayName:  strings.TrimSpace(req.DisplayName),
		ConfigState:  configState,
		AuthState:    authState,
		Availability: availability,
		Metadata:     req.Metadata,
	}
	return s.store.CreateAgentProviderProfile(ctx, profile)
}

func (s *Service) UpdateAgentProviderProfile(ctx context.Context, id string, req RequestUpdateAgentProviderProfile) (AgentProviderProfile, error) {
	profile, err := s.store.GetAgentProviderProfile(ctx, id)
	if err != nil {
		return AgentProviderProfile{}, err
	}
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			return AgentProviderProfile{}, ErrInvalidAgentProviderName
		}
		profile.Name = name
	}
	if req.DisplayName != nil {
		profile.DisplayName = strings.TrimSpace(*req.DisplayName)
	}
	if req.ConfigState != nil {
		v := strings.TrimSpace(*req.ConfigState)
		if !validAgentProviderConfigState(v) {
			return AgentProviderProfile{}, ErrInvalidAgentProviderConfigState
		}
		profile.ConfigState = v
	}
	if req.AuthState != nil {
		v := strings.TrimSpace(*req.AuthState)
		if !validAgentProviderAuthState(v) {
			return AgentProviderProfile{}, ErrInvalidAgentProviderAuthState
		}
		profile.AuthState = v
	}
	if req.Availability != nil {
		v := strings.TrimSpace(*req.Availability)
		if !validAgentProviderAvailability(v) {
			return AgentProviderProfile{}, ErrInvalidAgentProviderAvailability
		}
		profile.Availability = v
	}
	if req.LastProbeResult != nil {
		// 脱敏点:探测结果可能含外部返回的敏感片段。
		profile.LastProbeResult = safelog.Text(*req.LastProbeResult, 2000)
		profile.LastProbeAt = time.Now()
	}
	if req.Metadata != nil {
		profile.Metadata = req.Metadata // 整体替换
	}
	return s.store.UpdateAgentProviderProfile(ctx, profile)
}

// ============================ model profiles ============================

func (s *Service) ListAgentModelProfiles(ctx context.Context, filter AgentModelProfileListFilter) ([]AgentModelProfile, error) {
	return s.store.ListAgentModelProfiles(ctx, filter)
}

func (s *Service) GetAgentModelProfile(ctx context.Context, id string) (AgentModelProfile, error) {
	return s.store.GetAgentModelProfile(ctx, id)
}

func (s *Service) CreateAgentModelProfile(ctx context.Context, req RequestCreateAgentModelProfile) (AgentModelProfile, error) {
	if strings.TrimSpace(req.ProviderID) == "" {
		return AgentModelProfile{}, ErrAgentProviderNotFound
	}
	if _, err := s.store.GetAgentProviderProfile(ctx, req.ProviderID); err != nil {
		return AgentModelProfile{}, err
	}
	if strings.TrimSpace(req.ModelName) == "" {
		return AgentModelProfile{}, ErrInvalidAgentModelName
	}
	status := strings.TrimSpace(req.Status)
	if status == "" {
		status = AgentModelStatusAvailable
	} else if !validAgentModelStatus(status) {
		return AgentModelProfile{}, ErrInvalidAgentModelStatus
	}
	costLevel := strings.TrimSpace(req.CostLevel)
	if costLevel == "" {
		costLevel = AgentModelCostLevelMedium
	} else if !validAgentModelCostLevel(costLevel) {
		return AgentModelProfile{}, ErrInvalidAgentModelCostLevel
	}
	model := AgentModelProfile{
		ProviderID:      req.ProviderID,
		ModelName:       strings.TrimSpace(req.ModelName),
		DisplayName:     strings.TrimSpace(req.DisplayName),
		Enabled:         req.Enabled,
		Status:          status,
		CostLevel:       costLevel,
		ContextLimit:    req.ContextLimit,
		ConfirmRequired: req.ConfirmRequired,
		Metadata:        req.Metadata,
	}
	return s.store.CreateAgentModelProfile(ctx, model)
}

func (s *Service) UpdateAgentModelProfile(ctx context.Context, id string, req RequestUpdateAgentModelProfile) (AgentModelProfile, error) {
	model, err := s.store.GetAgentModelProfile(ctx, id)
	if err != nil {
		return AgentModelProfile{}, err
	}
	if req.DisplayName != nil {
		model.DisplayName = strings.TrimSpace(*req.DisplayName)
	}
	if req.Enabled != nil {
		model.Enabled = *req.Enabled
	}
	if req.Status != nil {
		v := strings.TrimSpace(*req.Status)
		if !validAgentModelStatus(v) {
			return AgentModelProfile{}, ErrInvalidAgentModelStatus
		}
		model.Status = v
	}
	if req.CostLevel != nil {
		v := strings.TrimSpace(*req.CostLevel)
		if !validAgentModelCostLevel(v) {
			return AgentModelProfile{}, ErrInvalidAgentModelCostLevel
		}
		model.CostLevel = v
	}
	if req.ContextLimit != nil {
		model.ContextLimit = *req.ContextLimit
	}
	if req.ConfirmRequired != nil {
		model.ConfirmRequired = *req.ConfirmRequired
	}
	if req.Metadata != nil {
		model.Metadata = req.Metadata
	}
	return s.store.UpdateAgentModelProfile(ctx, model)
}

// ============================ task profiles ============================

func (s *Service) ListAgentTaskProfiles(ctx context.Context, filter AgentTaskProfileListFilter) ([]AgentTaskProfile, error) {
	return s.store.ListAgentTaskProfiles(ctx, filter)
}

func (s *Service) GetAgentTaskProfile(ctx context.Context, id string) (AgentTaskProfile, error) {
	return s.store.GetAgentTaskProfile(ctx, id)
}

func (s *Service) GetAgentTaskProfileByType(ctx context.Context, taskType string) (AgentTaskProfile, error) {
	if !validAgentTaskType(taskType) {
		return AgentTaskProfile{}, ErrInvalidAgentTaskType
	}
	return s.store.GetAgentTaskProfileByType(ctx, taskType)
}

func (s *Service) UpdateAgentTaskProfile(ctx context.Context, taskType string, req RequestUpdateAgentTaskProfile) (AgentTaskProfile, error) {
	if !validAgentTaskType(taskType) {
		return AgentTaskProfile{}, ErrInvalidAgentTaskType
	}
	profile, err := s.store.GetAgentTaskProfileByType(ctx, taskType)
	if err != nil {
		return AgentTaskProfile{}, err
	}
	if req.PrimaryModelID != nil {
		primaryID := strings.TrimSpace(*req.PrimaryModelID)
		if primaryID != "" {
			if _, err := s.store.GetAgentModelProfile(ctx, primaryID); err != nil {
				return AgentTaskProfile{}, err // 防悬空绑定
			}
		}
		profile.PrimaryModelID = primaryID
	}
	if req.FallbackModelID != nil {
		fallbackID := strings.TrimSpace(*req.FallbackModelID)
		if fallbackID != "" {
			if _, err := s.store.GetAgentModelProfile(ctx, fallbackID); err != nil {
				return AgentTaskProfile{}, err
			}
		}
		profile.FallbackModelID = fallbackID
	}
	if req.ConfirmRequired != nil {
		profile.ConfirmRequired = *req.ConfirmRequired
	}
	if req.MaxBudget != nil {
		profile.MaxBudget = *req.MaxBudget
	}
	return s.store.UpdateAgentTaskProfile(ctx, profile)
}

// ============================ authorizations ============================

func (s *Service) ListAgentAuthorizations(ctx context.Context, filter AgentAuthorizationListFilter) ([]AgentAuthorization, error) {
	return s.store.ListAgentAuthorizations(ctx, filter)
}

func (s *Service) GetAgentAuthorization(ctx context.Context, id string) (AgentAuthorization, error) {
	return s.store.GetAgentAuthorization(ctx, id)
}

func (s *Service) ApproveAgentAuthorization(ctx context.Context, id string, req RequestAgentAuthorizationDecision) (AgentAuthorization, error) {
	return s.decideAgentAuthorization(ctx, id, AgentAuthorizationStatusApproved, req.DecisionReason)
}

func (s *Service) DenyAgentAuthorization(ctx context.Context, id string, req RequestAgentAuthorizationDecision) (AgentAuthorization, error) {
	return s.decideAgentAuthorization(ctx, id, AgentAuthorizationStatusDenied, req.DecisionReason)
}

// decideAgentAuthorization 推进授权状态。本轮 approve 不自动建 run;
// 后续真实调用轮次才在 approve 时触发 CreateAgentRunRecord。
func (s *Service) decideAgentAuthorization(ctx context.Context, id, status, decisionReason string) (AgentAuthorization, error) {
	auth, err := s.store.GetAgentAuthorization(ctx, id)
	if err != nil {
		return AgentAuthorization{}, err
	}
	if auth.Status != AgentAuthorizationStatusPending {
		return AgentAuthorization{}, ErrAgentAuthorizationAlreadyDecided
	}
	reason := safelog.Text(decisionReason, 1000) // 脱敏点
	return s.store.UpdateAgentAuthorizationDecision(ctx, id, status, reason)
}

// CreatePendingAuthorization 为高成本/高风险任务创建待授权记录。
func (s *Service) CreatePendingAuthorization(ctx context.Context, params AgentAuthorizationParams) (AgentAuthorization, error) {
	if !validAgentTaskType(params.TaskType) {
		return AgentAuthorization{}, ErrInvalidAgentTaskType
	}
	if _, err := s.store.GetAgentProviderProfile(ctx, params.ProviderID); err != nil {
		return AgentAuthorization{}, err
	}
	if _, err := s.store.GetAgentModelProfile(ctx, params.ModelID); err != nil {
		return AgentAuthorization{}, err
	}
	reason := safelog.Text(params.Reason, 1000) // 脱敏点
	auth := AgentAuthorization{
		TaskType:          params.TaskType,
		TaskProfileID:     params.TaskProfileID,
		ProviderID:        params.ProviderID,
		ModelID:           params.ModelID,
		TriggerObjectType: params.TriggerObjectType,
		TriggerObjectID:   params.TriggerObjectID,
		Status:            AgentAuthorizationStatusPending,
		Reason:            reason,
		RequestedBy:       params.RequestedBy,
	}
	return s.store.CreateAgentAuthorization(ctx, auth)
}

// ============================ runs + decision ledger ============================

func (s *Service) GetAgentRun(ctx context.Context, id string) (AgentRun, error) {
	return s.store.GetAgentRun(ctx, id)
}

func (s *Service) ListAgentRuns(ctx context.Context, filter AgentRunListFilter) ([]AgentRun, error) {
	return s.store.ListAgentRuns(ctx, filter)
}

func (s *Service) CountAgentRuns(ctx context.Context, filter AgentRunListFilter) (int, error) {
	return s.store.CountAgentRuns(ctx, filter)
}

func (s *Service) GetAgentDecisionLedger(ctx context.Context, id string) (AgentDecisionLedger, error) {
	return s.store.GetAgentDecisionLedger(ctx, id)
}

// CreateAgentRunRecord 创建 AgentRun 及其决策账本(事务原子),不真实调用模型,
// 不写假 output。敏感字段经 safelog 脱敏裁剪后落库,RedactionSummary 记录脱敏情况。
func (s *Service) CreateAgentRunRecord(ctx context.Context, params AgentRunRecordParams) (AgentRun, AgentDecisionLedger, error) {
	if !validAgentTaskType(params.TaskType) {
		return AgentRun{}, AgentDecisionLedger{}, ErrInvalidAgentTaskType
	}
	if _, err := s.store.GetAgentProviderProfile(ctx, params.ProviderID); err != nil {
		return AgentRun{}, AgentDecisionLedger{}, err
	}
	if _, err := s.store.GetAgentModelProfile(ctx, params.ModelID); err != nil {
		return AgentRun{}, AgentDecisionLedger{}, err
	}

	// 脱敏(信任边界,不可省):input 摘要 / prompt / input artifact 摘要。
	inputSummary := safelog.Text(params.InputSummary, 8192)
	prompt := safelog.Text(params.Prompt, 16384)
	inputArtifact := safelog.Text(params.InputArtifactSummary, 8192)
	redactionSummary := map[string]any{
		"pipeline":                     "safelog.Redact",
		"inputSummaryRedacted":         agentRedacted(params.InputSummary),
		"promptRedacted":               agentRedacted(params.Prompt),
		"inputArtifactSummaryRedacted": agentRedacted(params.InputArtifactSummary),
	}

	// 本轮不写假结论:Output 空串、StructuredOutput 空、CostEstimate 空。
	ledger := AgentDecisionLedger{
		ProviderID:            params.ProviderID,
		ModelID:               params.ModelID,
		TaskType:              params.TaskType,
		TriggerObjectType:     params.TriggerObjectType,
		TriggerObjectID:       params.TriggerObjectID,
		InputSummary:          inputSummary,
		Prompt:                prompt,
		InputArtifactSummary:  inputArtifact,
		StructuredOutput:      map[string]any{},
		RedactionSummary:      redactionSummary,
	}
	run := AgentRun{
		TaskType:          params.TaskType,
		ProviderID:        params.ProviderID,
		ModelID:           params.ModelID,
		TriggerObjectType: params.TriggerObjectType,
		TriggerObjectID:   params.TriggerObjectID,
		Status:            AgentRunStatusReady,
		CostEstimate:      map[string]any{},
		AuthorizationID:   params.AuthorizationID,
		StartedAt:         time.Now(),
	}
	return s.store.CreateAgentRunWithLedger(ctx, run, ledger)
}

// agentRedacted 判断原始文本经 safelog.Redact 后是否发生变化,用于留痕。
func agentRedacted(value string) bool {
	trimmed := strings.TrimSpace(value)
	return safelog.Redact(trimmed) != trimmed
}

// ============================ resolve ============================

// ResolveAgentTask 解析任务到模型:加载 task profile → 解析 primary/fallback model
// → confirm_required 时建 pending authorization(不建 run),否则建 run(+ledger)。
// 本轮不真实调用模型,不写假 output。
func (s *Service) ResolveAgentTask(ctx context.Context, taskType, triggerObjectType, triggerObjectID, requestedBy string) (AgentTaskResolution, error) {
	if !validAgentTaskType(taskType) {
		return AgentTaskResolution{}, ErrInvalidAgentTaskType
	}
	taskProfile, err := s.store.GetAgentTaskProfileByType(ctx, taskType)
	if err != nil {
		return AgentTaskResolution{}, err
	}
	model, err := s.resolveModel(ctx, taskProfile)
	if err != nil {
		return AgentTaskResolution{}, err
	}

	resolution := AgentTaskResolution{
		TaskType:          taskType,
		TaskProfileID:     taskProfile.ID,
		ProviderID:        model.ProviderID,
		ModelID:           model.ID,
		ModelName:         model.ModelName,
		TriggerObjectType: triggerObjectType,
		TriggerObjectID:   triggerObjectID,
		RequestedBy:       requestedBy,
	}

	if model.ConfirmRequired || taskProfile.ConfirmRequired {
		auth, err := s.CreatePendingAuthorization(ctx, AgentAuthorizationParams{
			TaskType:          taskType,
			TaskProfileID:     taskProfile.ID,
			ProviderID:        model.ProviderID,
			ModelID:           model.ID,
			TriggerObjectType: triggerObjectType,
			TriggerObjectID:   triggerObjectID,
			Reason:            "confirm required for task " + taskType,
			RequestedBy:       requestedBy,
		})
		if err != nil {
			return resolution, err
		}
		resolution.Status = AgentResolutionStatusPendingAuthorization
		resolution.PendingAuthorization = &auth
		return resolution, nil
	}

	run, ledger, err := s.CreateAgentRunRecord(ctx, AgentRunRecordParams{
		TaskType:          taskType,
		ProviderID:        model.ProviderID,
		ModelID:           model.ID,
		TriggerObjectType: triggerObjectType,
		TriggerObjectID:   triggerObjectID,
		RequestedBy:       requestedBy,
		InputSummary:      fmt.Sprintf("task_type=%s trigger=%s:%s (no model call this round)", taskType, triggerObjectType, triggerObjectID),
	})
	if err != nil {
		return resolution, err
	}
	resolution.Status = AgentResolutionStatusAuthorized
	resolution.Run = &run
	resolution.DecisionLedger = &ledger
	return resolution, nil
}

// resolveModel 优先 primary(Enabled && available),不可用则 fallback,都不可用则报错。
func (s *Service) resolveModel(ctx context.Context, taskProfile AgentTaskProfile) (AgentModelProfile, error) {
	tryModel := func(id string) (AgentModelProfile, bool) {
		if strings.TrimSpace(id) == "" {
			return AgentModelProfile{}, false
		}
		m, err := s.store.GetAgentModelProfile(ctx, id)
		if err != nil {
			return AgentModelProfile{}, false // 不存在或不可用 → 降级 fallback
		}
		if m.Enabled && m.Status == AgentModelStatusAvailable {
			return m, true
		}
		return AgentModelProfile{}, false
	}
	if m, ok := tryModel(taskProfile.PrimaryModelID); ok {
		return m, nil
	}
	if m, ok := tryModel(taskProfile.FallbackModelID); ok {
		return m, nil
	}
	return AgentModelProfile{}, ErrAgentModelNotAvailable
}

// ============================ counts ============================

func (s *Service) CountAgentProviderProfiles(ctx context.Context, filter AgentProviderProfileListFilter) (int, error) {
	return s.store.CountAgentProviderProfiles(ctx, filter)
}

func (s *Service) CountAgentModelProfiles(ctx context.Context, filter AgentModelProfileListFilter) (int, error) {
	return s.store.CountAgentModelProfiles(ctx, filter)
}

func (s *Service) CountAgentTaskProfiles(ctx context.Context, filter AgentTaskProfileListFilter) (int, error) {
	return s.store.CountAgentTaskProfiles(ctx, filter)
}

func (s *Service) CountAgentAuthorizations(ctx context.Context, filter AgentAuthorizationListFilter) (int, error) {
	return s.store.CountAgentAuthorizations(ctx, filter)
}
