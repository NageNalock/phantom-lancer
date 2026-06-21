package stockv2

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"phantom-lancer/internal/safelog"
)

// Agent 治理层 service。脱敏单点在此层(经 internal/safelog);store 只持久化,
// HTTP handler 不直接构造敏感字段。没有 executor 时只建 ready run,有 executor
// 时通过 Codex CLI + MCP 回填真实结果,不写假 AI 结论。

const (
	agentProviderMetadataBaseURL = "baseUrl"
	agentProviderMetadataAPIKey  = "apiKey"
	defaultOpenAIBaseURL         = "https://api.openai.com/v1"
	openAIProbeMaxBodyBytes      = 1 << 20
	codexModelCatalogTimeout     = 15 * time.Second
)

// ============================ provider profiles ============================

func (s *Service) ListAgentProviderProfiles(ctx context.Context, filter AgentProviderProfileListFilter) ([]AgentProviderProfile, error) {
	items, err := s.store.ListAgentProviderProfiles(ctx, filter)
	if err != nil {
		return nil, err
	}
	for i := range items {
		items[i] = sanitizeAgentProviderProfile(items[i])
	}
	return items, nil
}

func (s *Service) GetAgentProviderProfile(ctx context.Context, id string) (AgentProviderProfile, error) {
	profile, err := s.store.GetAgentProviderProfile(ctx, id)
	if err != nil {
		return AgentProviderProfile{}, err
	}
	return sanitizeAgentProviderProfile(profile), nil
}

func (s *Service) CreateAgentProviderProfile(ctx context.Context, req RequestCreateAgentProviderProfile) (AgentProviderProfile, error) {
	if strings.TrimSpace(req.ProviderType) == "" {
		req.ProviderType = AgentProviderTypeCodexCLI
	}
	if !validAgentProviderType(req.ProviderType) {
		return AgentProviderProfile{}, ErrInvalidAgentProviderType
	}
	if strings.TrimSpace(req.Name) == "" {
		req.Name = autoAgentProviderName(req.ProviderType)
	}
	configState := strings.TrimSpace(req.ConfigState)
	if configState == "" {
		configState = AgentProviderConfigStateConfigured
	} else if !validAgentProviderConfigState(configState) {
		return AgentProviderProfile{}, ErrInvalidAgentProviderConfigState
	}
	authState := strings.TrimSpace(req.AuthState)
	if authState == "" {
		if strings.TrimSpace(req.APIKey) != "" {
			authState = AgentProviderAuthStateAuthenticated
		} else {
			authState = AgentProviderAuthStateUnknown
		}
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
		Metadata:     mergeAgentProviderRuntimeMetadata(req.Metadata, req.BaseURL, req.APIKey),
	}
	created, err := s.store.CreateAgentProviderProfile(ctx, profile)
	if err != nil {
		return AgentProviderProfile{}, err
	}
	return sanitizeAgentProviderProfile(created), nil
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
	if req.BaseURL != nil || req.APIKey != nil {
		profile.Metadata = mergeAgentProviderRuntimeMetadata(profile.Metadata, valueOfStringPtr(req.BaseURL), valueOfStringPtr(req.APIKey))
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
		profile.Metadata = req.Metadata // 整体替换,兼容旧 API
		if req.BaseURL != nil || req.APIKey != nil {
			profile.Metadata = mergeAgentProviderRuntimeMetadata(profile.Metadata, valueOfStringPtr(req.BaseURL), valueOfStringPtr(req.APIKey))
		}
	}
	updated, err := s.store.UpdateAgentProviderProfile(ctx, profile)
	if err != nil {
		return AgentProviderProfile{}, err
	}
	return sanitizeAgentProviderProfile(updated), nil
}

func (s *Service) DeleteAgentProviderProfile(ctx context.Context, id string) error {
	profile, err := s.store.GetAgentProviderProfile(ctx, id)
	if err != nil {
		return err
	}
	if isDefaultCodexCLIProvider(profile) {
		return ErrAgentProviderProtected
	}
	return s.store.DeleteAgentProviderProfile(ctx, id)
}

// ============================ model profiles ============================

func (s *Service) ListAgentModelProfiles(ctx context.Context, filter AgentModelProfileListFilter) ([]AgentModelProfile, error) {
	return s.store.ListAgentModelProfiles(ctx, filter)
}

func (s *Service) GetAgentModelProfile(ctx context.Context, id string) (AgentModelProfile, error) {
	model, err := s.store.GetAgentModelProfile(ctx, id)
	if err != nil {
		return AgentModelProfile{}, err
	}
	return model, nil
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
		ProviderID:   req.ProviderID,
		ModelName:    strings.TrimSpace(req.ModelName),
		DisplayName:  strings.TrimSpace(req.DisplayName),
		Enabled:      req.Enabled,
		Status:       status,
		CostLevel:    costLevel,
		ContextLimit: req.ContextLimit,
		Metadata:     req.Metadata,
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
	if req.Metadata != nil {
		model.Metadata = req.Metadata
	}
	return s.store.UpdateAgentModelProfile(ctx, model)
}

func (s *Service) ListAgentProviderModels(ctx context.Context, providerID string) (AgentProviderModelCatalog, error) {
	profile, err := s.store.GetAgentProviderProfile(ctx, providerID)
	if err != nil {
		return AgentProviderModelCatalog{}, err
	}
	if isDefaultCodexCLIProvider(profile) {
		return s.listDefaultCodexCLIModels(ctx, profile)
	}
	baseURL, apiKey, err := agentProviderOpenAIConfig(profile)
	if err != nil {
		return AgentProviderModelCatalog{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/models", nil)
	if err != nil {
		return AgentProviderModelCatalog{}, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := s.agentHTTPClient().Do(req)
	if err != nil {
		s.recordAgentProviderProbe(ctx, profile, AgentProviderAvailabilityUnavailable, "model list request failed: "+err.Error())
		return AgentProviderModelCatalog{}, err
	}
	defer resp.Body.Close()

	body, readErr := io.ReadAll(io.LimitReader(resp.Body, openAIProbeMaxBodyBytes))
	if readErr != nil {
		s.recordAgentProviderProbe(ctx, profile, AgentProviderAvailabilityUnavailable, "model list response read failed: "+readErr.Error())
		return AgentProviderModelCatalog{}, readErr
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := fmt.Sprintf("model list returned %s: %s", resp.Status, safelog.Text(string(body), 600))
		s.recordAgentProviderProbe(ctx, profile, AgentProviderAvailabilityUnavailable, message)
		return AgentProviderModelCatalog{}, fmt.Errorf("%s", message)
	}

	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		s.recordAgentProviderProbe(ctx, profile, AgentProviderAvailabilityDegraded, "model list response is not OpenAI-compatible JSON")
		return AgentProviderModelCatalog{}, err
	}
	items := make([]AgentProviderModelCatalogItem, 0, len(payload.Data))
	for _, item := range payload.Data {
		id := strings.TrimSpace(item.ID)
		if id == "" {
			continue
		}
		items = append(items, AgentProviderModelCatalogItem{ID: id, DisplayName: id})
	}
	s.recordAgentProviderProbe(ctx, profile, AgentProviderAvailabilityAvailable, fmt.Sprintf("model list ok: %d models", len(items)))
	return AgentProviderModelCatalog{ProviderID: providerID, Items: items}, nil
}

func (s *Service) TestAgentModel(ctx context.Context, req RequestTestAgentModel) (AgentModelTestResult, error) {
	providerID := strings.TrimSpace(req.ProviderID)
	modelName := strings.TrimSpace(req.ModelName)
	if providerID == "" {
		return AgentModelTestResult{}, ErrAgentProviderNotFound
	}
	if modelName == "" {
		return AgentModelTestResult{}, ErrInvalidAgentModelName
	}
	profile, err := s.store.GetAgentProviderProfile(ctx, providerID)
	if err != nil {
		return AgentModelTestResult{}, err
	}
	if isDefaultCodexCLIProvider(profile) {
		return s.testDefaultCodexCLIModel(ctx, profile, modelName), nil
	}
	baseURL, apiKey, err := agentProviderOpenAIConfig(profile)
	if err != nil {
		return AgentModelTestResult{}, err
	}
	body := map[string]any{
		"model": modelName,
		"messages": []map[string]string{
			{"role": "user", "content": "ping"},
		},
		"max_tokens": 1,
	}
	payload, _ := json.Marshal(body)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return AgentModelTestResult{}, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	start := time.Now()
	resp, err := s.agentHTTPClient().Do(httpReq)
	latencyMS := time.Since(start).Milliseconds()
	if err != nil {
		message := "model test request failed: " + safelog.Text(err.Error(), 600)
		s.recordAgentProviderProbe(ctx, profile, AgentProviderAvailabilityUnavailable, message)
		s.markAgentModelStatus(ctx, providerID, modelName, AgentModelStatusUnavailable)
		return AgentModelTestResult{OK: false, Message: message, LatencyMS: latencyMS}, nil
	}
	defer resp.Body.Close()

	respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, openAIProbeMaxBodyBytes))
	if readErr != nil {
		message := "model test response read failed: " + safelog.Text(readErr.Error(), 600)
		s.recordAgentProviderProbe(ctx, profile, AgentProviderAvailabilityUnavailable, message)
		s.markAgentModelStatus(ctx, providerID, modelName, AgentModelStatusUnavailable)
		return AgentModelTestResult{OK: false, Message: message, LatencyMS: latencyMS}, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := fmt.Sprintf("model test returned %s: %s", resp.Status, safelog.Text(string(respBody), 600))
		s.recordAgentProviderProbe(ctx, profile, AgentProviderAvailabilityUnavailable, message)
		s.markAgentModelStatus(ctx, providerID, modelName, AgentModelStatusUnavailable)
		return AgentModelTestResult{OK: false, Message: message, LatencyMS: latencyMS}, nil
	}

	message := "model test ok"
	s.recordAgentProviderProbe(ctx, profile, AgentProviderAvailabilityAvailable, message)
	s.markAgentModelStatus(ctx, providerID, modelName, AgentModelStatusAvailable)
	return AgentModelTestResult{OK: true, Message: message, LatencyMS: latencyMS}, nil
}

func (s *Service) listDefaultCodexCLIModels(ctx context.Context, profile AgentProviderProfile) (AgentProviderModelCatalog, error) {
	items, source, err := s.fetchCodexCLIModelCatalog(ctx, false)
	if err == nil {
		s.recordAgentProviderProbe(ctx, profile, AgentProviderAvailabilityAvailable, fmt.Sprintf("codex cli model list ok: %d models", len(items)))
		return AgentProviderModelCatalog{ProviderID: profile.ID, Items: items}, nil
	}

	items, source, fallbackErr := s.fetchCodexCLIModelCatalog(ctx, true)
	if fallbackErr != nil {
		message := "codex cli model list failed: " + safelog.Text(err.Error()+"; "+fallbackErr.Error(), 700)
		s.recordAgentProviderProbe(ctx, profile, AgentProviderAvailabilityUnavailable, message)
		return AgentProviderModelCatalog{}, fmt.Errorf("%s", message)
	}
	s.recordAgentProviderProbe(ctx, profile, AgentProviderAvailabilityDegraded, fmt.Sprintf("codex cli live catalog failed, bundled catalog ok: %d models", len(items)))
	for i := range items {
		items[i].Source = source
	}
	return AgentProviderModelCatalog{ProviderID: profile.ID, Items: items}, nil
}

func (s *Service) fetchCodexCLIModelCatalog(ctx context.Context, bundled bool) ([]AgentProviderModelCatalogItem, string, error) {
	if s.agentCodexCommand == nil {
		return nil, "", ErrAgentExecutorUnavailable
	}
	args := []string{"debug", "models"}
	source := "codex_cli"
	if bundled {
		args = append(args, "--bundled")
		source = "codex_cli_bundled"
	}
	cmdCtx, cancel := context.WithTimeout(ctx, codexModelCatalogTimeout)
	defer cancel()
	output, err := s.agentCodexCommand(cmdCtx, args...)
	if err != nil {
		return nil, source, fmt.Errorf("codex debug models failed: %s", safelog.Text(string(output)+" "+err.Error(), 700))
	}
	items, err := parseCodexCLIModelCatalog(output, source)
	if err != nil {
		return nil, source, err
	}
	if len(items) == 0 {
		return nil, source, errors.New("codex model catalog is empty")
	}
	return items, source, nil
}

func parseCodexCLIModelCatalog(output []byte, source string) ([]AgentProviderModelCatalogItem, error) {
	idx := bytes.IndexByte(output, '{')
	if idx < 0 {
		return nil, errors.New("codex model catalog response missing JSON object")
	}
	var payload struct {
		Models []struct {
			Slug           string `json:"slug"`
			ID             string `json:"id"`
			Name           string `json:"name"`
			DisplayName    string `json:"display_name"`
			Visibility     string `json:"visibility"`
			SupportedInAPI bool   `json:"supported_in_api"`
		} `json:"models"`
	}
	if err := json.NewDecoder(bytes.NewReader(bytes.TrimSpace(output[idx:]))).Decode(&payload); err != nil {
		return nil, err
	}
	items := make([]AgentProviderModelCatalogItem, 0, len(payload.Models))
	for _, model := range payload.Models {
		if strings.EqualFold(strings.TrimSpace(model.Visibility), "hide") {
			continue
		}
		id := strings.TrimSpace(model.Slug)
		if id == "" {
			id = strings.TrimSpace(model.ID)
		}
		if id == "" {
			id = strings.TrimSpace(model.Name)
		}
		if id == "" {
			continue
		}
		displayName := strings.TrimSpace(model.DisplayName)
		if displayName == "" {
			displayName = id
		}
		items = append(items, AgentProviderModelCatalogItem{
			ID:             id,
			DisplayName:    displayName,
			Visibility:     strings.TrimSpace(model.Visibility),
			SupportedInAPI: model.SupportedInAPI,
			Source:         source,
		})
	}
	return items, nil
}

func (s *Service) testDefaultCodexCLIModel(ctx context.Context, profile AgentProviderProfile, modelName string) AgentModelTestResult {
	start := time.Now()
	catalog, err := s.listDefaultCodexCLIModels(ctx, profile)
	latencyMS := time.Since(start).Milliseconds()
	if err != nil {
		s.markAgentModelStatus(ctx, profile.ID, modelName, AgentModelStatusUnavailable)
		return AgentModelTestResult{OK: false, Message: safelog.Text(err.Error(), 700), LatencyMS: latencyMS}
	}
	for _, item := range catalog.Items {
		if item.ID == modelName {
			s.markAgentModelStatus(ctx, profile.ID, modelName, AgentModelStatusAvailable)
			return AgentModelTestResult{OK: true, Message: "codex cli model found in catalog", LatencyMS: latencyMS}
		}
	}
	s.markAgentModelStatus(ctx, profile.ID, modelName, AgentModelStatusUnavailable)
	return AgentModelTestResult{OK: false, Message: "model not found in codex cli catalog", LatencyMS: latencyMS}
}

// ============================ task profiles ============================

func (s *Service) ListAgentTaskProfiles(ctx context.Context, filter AgentTaskProfileListFilter) ([]AgentTaskProfile, error) {
	return s.store.ListAgentTaskProfiles(ctx, filter)
}

func (s *Service) GetAgentTaskProfile(ctx context.Context, id string) (AgentTaskProfile, error) {
	profile, err := s.store.GetAgentTaskProfile(ctx, id)
	if err != nil {
		return AgentTaskProfile{}, err
	}
	return profile, nil
}

func (s *Service) GetAgentTaskProfileByType(ctx context.Context, taskType string) (AgentTaskProfile, error) {
	if !knownAgentTaskType(taskType) {
		return AgentTaskProfile{}, ErrInvalidAgentTaskType
	}
	profile, err := s.store.GetAgentTaskProfileByType(ctx, taskType)
	if err != nil {
		return AgentTaskProfile{}, err
	}
	return profile, nil
}

func (s *Service) UpdateAgentTaskProfile(ctx context.Context, taskType string, req RequestUpdateAgentTaskProfile) (AgentTaskProfile, error) {
	if !knownAgentTaskType(taskType) {
		return AgentTaskProfile{}, ErrInvalidAgentTaskType
	}
	if !executableAgentTaskType(taskType) {
		return AgentTaskProfile{}, ErrAgentTaskNotConfigurable
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
	if req.MaxBudget != nil {
		profile.MaxBudget = *req.MaxBudget
	}
	return s.store.UpdateAgentTaskProfile(ctx, profile)
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

func (s *Service) GetAgentExecutionDetail(ctx context.Context, runID string) (AgentExecutionDetail, error) {
	run, err := s.store.GetAgentRun(ctx, runID)
	if err != nil {
		return AgentExecutionDetail{}, err
	}
	return s.agentExecutionDetailForRun(ctx, run)
}

func (s *Service) ListMonitorRunAgentExecutionDetails(ctx context.Context, monitorRunID string) ([]AgentExecutionDetail, error) {
	if _, err := s.store.GetMonitorRun(ctx, monitorRunID); err != nil {
		return nil, err
	}
	out := make([]AgentExecutionDetail, 0)

	directRuns, err := s.store.ListAgentRuns(ctx, AgentRunListFilter{
		TriggerObjectType: "monitor_run",
		TriggerObjectID:   monitorRunID,
		Limit:             50,
	})
	if err != nil {
		return nil, err
	}
	for _, run := range directRuns {
		detail, err := s.agentExecutionDetailForRun(ctx, run)
		if err != nil {
			return nil, err
		}
		out = append(out, detail)
	}

	reviews, err := s.store.ListOperationReviews(ctx, OperationReviewListFilter{
		RunID: monitorRunID,
		Limit: 200,
	})
	if err != nil {
		return nil, err
	}
	for _, review := range reviews {
		runs, err := s.store.ListAgentRuns(ctx, AgentRunListFilter{
			TaskType:          AgentTaskTypeOperationReview,
			TriggerObjectType: "operation_review",
			TriggerObjectID:   review.ID,
			Limit:             20,
		})
		if err != nil {
			return nil, err
		}
		for _, run := range runs {
			detail, err := s.agentExecutionDetailForRun(ctx, run)
			if err != nil {
				return nil, err
			}
			if detail.Review == nil {
				reviewCopy := review
				detail.Review = &reviewCopy
				detail.InputContext = &reviewCopy.InputContext
			}
			out = append(out, detail)
		}
	}
	return out, nil
}

func (s *Service) agentExecutionDetailForRun(ctx context.Context, run AgentRun) (AgentExecutionDetail, error) {
	detail := AgentExecutionDetail{Run: run}
	if run.DecisionLedgerID != "" {
		ledger, err := s.store.GetAgentDecisionLedger(ctx, run.DecisionLedgerID)
		if err != nil && !errors.Is(err, ErrAgentDecisionLedgerNotFound) {
			return AgentExecutionDetail{}, err
		}
		if err == nil {
			detail.Ledger = &ledger
		}
	}
	if run.TriggerObjectType == "operation_review" && run.TriggerObjectID != "" {
		review, err := s.store.GetOperationReview(ctx, run.TriggerObjectID)
		if err != nil && !errors.Is(err, ErrOperationReviewNotFound) {
			return AgentExecutionDetail{}, err
		}
		if err == nil {
			detail.Review = &review
			detail.InputContext = &review.InputContext
		}
	}
	return detail, nil
}

// CreateAgentRunRecord 创建 AgentRun 及其决策账本(事务原子)。这里只创建 ready
// 记录,真实 executor 的 stdout/MCP 结果由后续 finalize 写回。敏感字段经
// safelog 脱敏裁剪后落库,RedactionSummary 记录脱敏情况。
func (s *Service) CreateAgentRunRecord(ctx context.Context, params AgentRunRecordParams) (AgentRun, AgentDecisionLedger, error) {
	if !knownAgentTaskType(params.TaskType) {
		return AgentRun{}, AgentDecisionLedger{}, ErrInvalidAgentTaskType
	}
	if !executableAgentTaskType(params.TaskType) {
		return AgentRun{}, AgentDecisionLedger{}, ErrAgentTaskNotConfigurable
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

	// ready 阶段不写假结论:Output 空串、StructuredOutput 空、CostEstimate 空。
	ledger := AgentDecisionLedger{
		ProviderID:           params.ProviderID,
		ModelID:              params.ModelID,
		TaskType:             params.TaskType,
		TriggerObjectType:    params.TriggerObjectType,
		TriggerObjectID:      params.TriggerObjectID,
		InputSummary:         inputSummary,
		Prompt:               prompt,
		InputArtifactSummary: inputArtifact,
		StructuredOutput:     map[string]any{},
		RedactionSummary:     redactionSummary,
	}
	run := AgentRun{
		TaskType:          params.TaskType,
		ProviderID:        params.ProviderID,
		ModelID:           params.ModelID,
		TriggerObjectType: params.TriggerObjectType,
		TriggerObjectID:   params.TriggerObjectID,
		Status:            AgentRunStatusReady,
		CostEstimate:      map[string]any{},
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

// ResolveAgentTask 解析任务到模型:加载 task profile → 解析 primary/fallback model → 建 run(+ledger)。
func (s *Service) ResolveAgentTask(ctx context.Context, taskType, triggerObjectType, triggerObjectID, requestedBy string) (AgentTaskResolution, error) {
	if !knownAgentTaskType(taskType) {
		return AgentTaskResolution{}, ErrInvalidAgentTaskType
	}
	if !executableAgentTaskType(taskType) {
		return AgentTaskResolution{}, ErrAgentTaskNotConfigurable
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

	run, ledger, err := s.CreateAgentRunRecord(ctx, AgentRunRecordParams{
		TaskType:          taskType,
		ProviderID:        model.ProviderID,
		ModelID:           model.ID,
		TriggerObjectType: triggerObjectType,
		TriggerObjectID:   triggerObjectID,
		RequestedBy:       requestedBy,
		InputSummary:      fmt.Sprintf("task_type=%s trigger=%s:%s", taskType, triggerObjectType, triggerObjectID),
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

func (s *Service) agentHTTPClient() *http.Client {
	if s.httpClient != nil {
		return s.httpClient
	}
	return http.DefaultClient
}

func (s *Service) recordAgentProviderProbe(ctx context.Context, profile AgentProviderProfile, availability, summary string) {
	if !validAgentProviderAvailability(availability) {
		availability = AgentProviderAvailabilityUnknown
	}
	profile.Availability = availability
	profile.LastProbeAt = time.Now()
	profile.LastProbeResult = safelog.Text(summary, 1000)
	if _, err := s.store.UpdateAgentProviderProfile(ctx, profile); err != nil && s.log != nil {
		s.log.Warn("record stockv2 agent provider probe failed", "provider_id", profile.ID, "error", safelog.Text(err.Error(), 300))
	}
}

func (s *Service) markAgentModelStatus(ctx context.Context, providerID, modelName, status string) {
	if !validAgentModelStatus(status) {
		return
	}
	items, err := s.store.ListAgentModelProfiles(ctx, AgentModelProfileListFilter{
		ProviderID: providerID,
		Limit:      1000,
	})
	if err != nil {
		return
	}
	for _, model := range items {
		if model.ModelName != modelName {
			continue
		}
		model.Status = status
		if _, err := s.store.UpdateAgentModelProfile(ctx, model); err != nil && s.log != nil {
			s.log.Warn("mark stockv2 agent model status failed", "model_id", model.ID, "error", safelog.Text(err.Error(), 300))
		}
	}
}

func sanitizeAgentProviderProfile(profile AgentProviderProfile) AgentProviderProfile {
	if isDefaultCodexCLIProvider(profile) {
		profile.BaseURL = ""
		profile.APIKeySet = false
	} else {
		profile.BaseURL = agentProviderBaseURL(profile)
		profile.APIKeySet = agentProviderAPIKey(profile) != ""
	}
	profile.Metadata = nil
	return profile
}

func isDefaultCodexCLIProvider(profile AgentProviderProfile) bool {
	return profile.ProviderType == AgentProviderTypeCodexCLI && profile.ID == agentProviderCodexCLIDefaultID
}

func agentProviderOpenAIConfig(profile AgentProviderProfile) (string, string, error) {
	baseURL := agentProviderBaseURL(profile)
	if strings.TrimSpace(baseURL) == "" {
		return "", "", ErrAgentProviderBaseURLRequired
	}
	apiKey := agentProviderAPIKey(profile)
	if strings.TrimSpace(apiKey) == "" {
		return "", "", ErrAgentProviderAPIKeyRequired
	}
	return baseURL, apiKey, nil
}

func agentProviderBaseURL(profile AgentProviderProfile) string {
	if strings.TrimSpace(profile.BaseURL) != "" {
		return strings.TrimSpace(profile.BaseURL)
	}
	if value, ok := metadataString(profile.Metadata, agentProviderMetadataBaseURL); ok && value != "" {
		return value
	}
	return defaultOpenAIBaseURL
}

func agentProviderAPIKey(profile AgentProviderProfile) string {
	if value, ok := metadataString(profile.Metadata, agentProviderMetadataAPIKey); ok {
		return value
	}
	return ""
}

func mergeAgentProviderRuntimeMetadata(metadata map[string]any, baseURL, apiKey string) map[string]any {
	out := make(map[string]any, len(metadata)+2)
	for k, v := range metadata {
		out[k] = v
	}
	if trimmed := strings.TrimSpace(baseURL); trimmed != "" {
		out[agentProviderMetadataBaseURL] = trimmed
	}
	if trimmed := strings.TrimSpace(apiKey); trimmed != "" {
		out[agentProviderMetadataAPIKey] = trimmed
	}
	return out
}

func metadataString(metadata map[string]any, key string) (string, bool) {
	if metadata == nil {
		return "", false
	}
	value, ok := metadata[key]
	if !ok {
		return "", false
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed), true
	default:
		return strings.TrimSpace(fmt.Sprint(typed)), true
	}
}

func autoAgentProviderName(providerType string) string {
	id := generateID()
	if len(id) > 8 {
		id = id[:8]
	}
	return strings.TrimSpace(providerType) + "-" + id
}

func valueOfStringPtr(value *string) string {
	if value == nil {
		return ""
	}
	return *value
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

// ============================ execution integration ============================

func (s *Service) RunAgentCLIDebug(ctx context.Context, req RequestRunAgentCLIDebug) (AgentExecutionDetail, error) {
	if s.agentExecutor == nil {
		return AgentExecutionDetail{}, ErrAgentExecutorUnavailable
	}
	modelID := strings.TrimSpace(req.ModelID)
	if modelID == "" {
		return AgentExecutionDetail{}, ErrAgentModelNotFound
	}
	model, err := s.store.GetAgentModelProfile(ctx, modelID)
	if err != nil {
		return AgentExecutionDetail{}, err
	}
	if !model.Enabled || model.Status != AgentModelStatusAvailable {
		return AgentExecutionDetail{}, ErrAgentModelNotAvailable
	}

	triggerID := "debug-" + generateID()
	pack := AgentContextPack{
		BuiltAt: time.Now(),
		Hit: MonitorHit{
			ID:       triggerID,
			TaskType: "agent_cli_debug",
			Status:   MonitorHitStatusCandidate,
			Title:    "Agent CLI debug self check",
			Summary:  "Verify Codex CLI execution, stdout capture, and MCP submit_result callback.",
			Evidence: map[string]any{
				"debug":              true,
				"expectedOutputType": OperationReviewOutputContinueMonitoring,
				"instruction":        "Return continue_monitoring after confirming the MCP callback path works.",
			},
		},
		Evidence: map[string]any{"debug": true},
		Freshness: map[string]any{
			"generatedAt": time.Now().Format(time.RFC3339),
			"purpose":     "agent_cli_debug",
		},
	}
	inputArtifact, _ := json.Marshal(map[string]any{
		"debug":   true,
		"task":    "codex_cli_mcp_roundtrip",
		"context": pack,
	})
	run, ledger, err := s.CreateAgentRunRecord(ctx, AgentRunRecordParams{
		TaskType:             AgentTaskTypeOperationReview,
		ProviderID:           model.ProviderID,
		ModelID:              model.ID,
		TriggerObjectType:    "agent_cli_debug",
		TriggerObjectID:      triggerID,
		RequestedBy:          req.RequestedBy,
		InputSummary:         "CLI debug: verify Codex CLI stdout capture and MCP submit_result callback.",
		InputArtifactSummary: string(inputArtifact),
	})
	if err != nil {
		return AgentExecutionDetail{}, err
	}
	if _, _, err := s.executeAgentRun(ctx, run, ledger, pack, model.ModelName); err != nil {
		detail, detailErr := s.GetAgentExecutionDetail(ctx, run.ID)
		if detailErr == nil {
			return detail, nil
		}
		return AgentExecutionDetail{}, err
	}
	return s.GetAgentExecutionDetail(ctx, run.ID)
}

// RunAgentReviewForReview 从 OperationReview 启动一次 Agent Review 运行。
// 幂等:如果已经有非终态的关联 run,直接返回。
func (s *Service) RunAgentReviewForReview(ctx context.Context, reviewID string, requestedBy string) (AgentRun, error) {
	// 1. 找 review
	review, err := s.store.GetOperationReview(ctx, reviewID)
	if err != nil {
		return AgentRun{}, err
	}

	// 2. 幂等检查: trigger=operation_review/reviewID 的非终态 run
	filter := AgentRunListFilter{
		TaskType:          AgentTaskTypeOperationReview,
		TriggerObjectType: "operation_review",
		TriggerObjectID:   reviewID,
		Limit:             1,
	}
	existing, err := s.store.ListAgentRuns(ctx, filter)
	if err == nil && len(existing) > 0 {
		run := existing[0]
		if run.Status != AgentRunStatusCompleted && run.Status != AgentRunStatusFailed {
			return run, nil
		}
	}

	// 3. resolve (直接建 run;旧确认闸已从 Stock V2 主链路移除)
	resolution, err := s.ResolveAgentTask(
		ctx,
		AgentTaskTypeOperationReview,
		"operation_review",
		reviewID,
		requestedBy,
	)
	if err != nil {
		return AgentRun{}, err
	}
	if resolution.Run == nil {
		return AgentRun{}, fmt.Errorf("no run created")
	}

	// 4. 如果是 authorized 且有 executor,异步启动执行
	if s.agentExecutor != nil && resolution.Status == AgentResolutionStatusAuthorized {
		go s.startAgentRunAsync(
			context.Background(), // 独立生命周期
			*resolution.Run,
			*resolution.DecisionLedger,
			review.InputContext,
			resolution.ModelName,
		)
	}

	return *resolution.Run, nil
}

// startAgentRunAsync 异步启动 Agent 执行。
// 完成后调 finalizeAgentRun 落库。panic 有 recover。
func (s *Service) startAgentRunAsync(
	ctx context.Context,
	run AgentRun,
	ledger AgentDecisionLedger,
	pack AgentContextPack,
	modelName string,
) {
	defer func() {
		if r := recover(); r != nil {
			if s.log != nil {
				s.log.Error("agent run panicked", "run_id", run.ID, "panic", r)
			}
			s.finalizeAgentRun(ctx, run.ID, nil, fmt.Errorf("panic: %v", r))
		}
	}()

	if s.agentExecutor == nil {
		s.finalizeAgentRun(ctx, run.ID, nil, fmt.Errorf("no executor configured"))
		return
	}

	if _, _, err := s.executeAgentRun(ctx, run, ledger, pack, modelName); err != nil && s.log != nil {
		s.log.Warn("agent run finished with error", "run_id", run.ID, "error", safelog.Text(err.Error(), 300))
	}
}

func (s *Service) executeAgentRun(
	ctx context.Context,
	run AgentRun,
	ledger AgentDecisionLedger,
	pack AgentContextPack,
	modelName string,
) (AgentRun, AgentDecisionLedger, error) {
	if s.agentExecutor == nil {
		s.finalizeAgentRun(ctx, run.ID, nil, fmt.Errorf("no executor configured"))
		finalRun, finalLedger := s.safeGetAgentRunAndLedger(ctx, run.ID, ledger.ID)
		return finalRun, finalLedger, ErrAgentExecutorUnavailable
	}

	running := run
	running.Status = AgentRunStatusRunning
	if _, err := s.store.UpdateAgentRun(ctx, running); err != nil {
		if s.log != nil {
			s.log.Warn("update agent run to running failed", "run_id", run.ID, "error", err)
		}
	}

	taskID, _ := s.agentTaskPool.createTask(run.TaskType, run.ID, "", 10*time.Minute)

	execOutput, execErr := s.agentExecutor.ExecuteOperationReview(ctx, taskID, pack, modelName)

	s.finalizeAgentRunWithOutput(ctx, run.ID, ledger.ID, taskID, execOutput, execErr)
	finalRun, finalLedger := s.safeGetAgentRunAndLedger(ctx, run.ID, ledger.ID)
	return finalRun, finalLedger, execErr
}

func (s *Service) safeGetAgentRunAndLedger(ctx context.Context, runID, ledgerID string) (AgentRun, AgentDecisionLedger) {
	run, _ := s.store.GetAgentRun(ctx, runID)
	if ledgerID == "" {
		ledgerID = run.DecisionLedgerID
	}
	ledger, _ := s.store.GetAgentDecisionLedger(ctx, ledgerID)
	return run, ledger
}

// finalizeAgentRun 用 execErr 标记失败。
func (s *Service) finalizeAgentRun(ctx context.Context, runID string, output *AgentExecutorOutput, execErr error) {
	s.finalizeAgentRunWithOutput(ctx, runID, "", "", output, execErr)
}

// finalizeAgentRunWithOutput 执行完成后统一落库。
// 1. 从 task pool 读 result
// 2. 校验 schema
// 3. 写 ledger + run + review result(跑 guardrails)
func (s *Service) finalizeAgentRunWithOutput(
	ctx context.Context,
	runID, ledgerID, taskID string,
	execOutput *AgentExecutorOutput,
	execErr error,
) {
	// 读 task result
	var submitted *AgentTaskSubmittedResult
	if taskID != "" {
		if entry, ok := s.agentTaskPool.getTask(taskID); ok {
			entry.mu.Lock()
			submitted = entry.submittedResult
			entry.mu.Unlock()
		}
		// 消费后移除
		s.agentTaskPool.remove(taskID)
	}

	// 取 run 和 ledger
	run, err := s.store.GetAgentRun(ctx, runID)
	if err != nil {
		if s.log != nil {
			s.log.Error("finalize: get run failed", "run_id", runID, "error", err)
		}
		return
	}

	if ledgerID == "" {
		ledgerID = run.DecisionLedgerID
	}
	ledger, err := s.store.GetAgentDecisionLedger(ctx, ledgerID)
	if err != nil {
		if s.log != nil {
			s.log.Warn("finalize: get ledger failed", "run_id", runID, "ledger_id", ledgerID, "error", err)
		}
	}

	// 准备 output artifact summary
	var outputArtifact strings.Builder
	if execOutput != nil {
		fmt.Fprintf(&outputArtifact, "exit_code: %d\n", execOutput.ExitCode)
		fmt.Fprintf(&outputArtifact, "duration: %s\n", execOutput.Duration)
		if execOutput.TimedOut {
			outputArtifact.WriteString("timed_out: true\n")
		}
		if execOutput.StdoutTail != "" {
			outputArtifact.WriteString("stdout_tail:\n")
			outputArtifact.WriteString(execOutput.StdoutTail)
		}
		if execOutput.StderrTail != "" {
			outputArtifact.WriteString("stderr_tail:\n")
			outputArtifact.WriteString(execOutput.StderrTail)
		}
	}

	// 判断成功还是失败
	hasValidResult := submitted != nil && submitted.OutputType != "" &&
		validOperationReviewOutputType(submitted.OutputType)

	if execErr != nil && !hasValidResult {
		// 失败路径
		run.Status = AgentRunStatusFailed
		run.ErrorMessage = safelog.Text(execErr.Error(), 500)
		now := time.Now()
		run.FinishedAt = now

		ledger.OutputArtifactSummary = safelog.Text(outputArtifact.String(), 16384)

		if _, err := s.store.UpdateAgentRun(ctx, run); err != nil {
			s.log.Warn("finalize: update run failed", "run_id", runID, "error", err)
		}
		if _, err := s.store.UpdateAgentDecisionLedger(ctx, ledger); err != nil {
			s.log.Warn("finalize: update ledger failed", "run_id", runID, "error", err)
		}
		return
	}

	// 成功路径
	if !hasValidResult {
		// 进程退出了但没提交 result,也算失败
		run.Status = AgentRunStatusFailed
		run.ErrorMessage = "no valid result submitted"
		run.FinishedAt = time.Now()
		ledger.OutputArtifactSummary = safelog.Text(outputArtifact.String(), 16384)
		s.store.UpdateAgentRun(ctx, run)
		s.store.UpdateAgentDecisionLedger(ctx, ledger)
		return
	}

	// 有合法 result
	run.Status = AgentRunStatusCompleted
	run.Output = safelog.Text(submitted.ResultSummary, 2000)
	run.FinishedAt = time.Now()

	// 更新 ledger
	ledger.OutputArtifactSummary = safelog.Text(outputArtifact.String(), 16384)
	ledger.StructuredOutput = map[string]any{
		"outputType":    submitted.OutputType,
		"resultSummary": submitted.ResultSummary,
		"result":        submitted.Result,
		"confidence":    submitted.Confidence,
	}
	// 更新 redaction summary
	redacted := make(map[string]any)
	if ledger.RedactionSummary != nil {
		for k, v := range ledger.RedactionSummary {
			redacted[k] = v
		}
	}
	redacted["outputArtifactSummaryRedacted"] = agentRedacted(outputArtifact.String())
	redacted["structuredOutputRedacted"] = false // 结构化结果不跑全文脱敏
	ledger.RedactionSummary = redacted

	if _, err := s.store.UpdateAgentRun(ctx, run); err != nil {
		s.log.Warn("finalize: update run failed", "run_id", runID, "error", err)
	}
	if _, err := s.store.UpdateAgentDecisionLedger(ctx, ledger); err != nil {
		s.log.Warn("finalize: update ledger failed", "run_id", runID, "error", err)
	}

	// 更新 Review result(自动跑 guardrails)
	if run.TriggerObjectType == "operation_review" && run.TriggerObjectID != "" {
		reviewID := run.TriggerObjectID
		saveReq := RequestSaveOperationReviewResult{
			OutputType:    submitted.OutputType,
			Result:        submitted.Result,
			ResultSummary: submitted.ResultSummary,
			Status:        OperationReviewStatusCompleted,
		}
		if _, err := s.SaveOperationReviewResult(ctx, reviewID, saveReq); err != nil {
			run.Status = AgentRunStatusFailed
			run.ErrorMessage = safelog.Text("save review result failed: "+err.Error(), 500)
			if _, updateErr := s.store.UpdateAgentRun(ctx, run); updateErr != nil && s.log != nil {
				s.log.Warn("finalize: update run after review save failed", "run_id", runID, "error", updateErr)
			}
			if s.log != nil {
				s.log.Warn("finalize: save review result failed", "run_id", runID, "review_id", reviewID, "error", err)
			}
		}
	}
}

// validOperationReviewOutputType 在 review_service.go 中定义
