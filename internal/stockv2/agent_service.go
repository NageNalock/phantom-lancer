package stockv2

import (
	"bytes"
	"context"
	"encoding/base64"
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
		ProviderID:          req.ProviderID,
		ModelName:           strings.TrimSpace(req.ModelName),
		DisplayName:         strings.TrimSpace(req.DisplayName),
		Enabled:             req.Enabled,
		Status:              status,
		CostLevel:           costLevel,
		ContextLimit:        req.ContextLimit,
		ModelType:           req.ModelType,
		EmbeddingProtocol:   req.EmbeddingProtocol,
		EmbeddingDimensions: req.EmbeddingDimensions,
		InputModalities:     req.InputModalities,
		EncodingFormat:      req.EncodingFormat,
		Metadata:            req.Metadata,
	}
	if err := normalizeAgentModelRuntimeFields(&model); err != nil {
		return AgentModelProfile{}, err
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
	if req.ModelType != nil {
		model.ModelType = strings.TrimSpace(*req.ModelType)
	}
	if req.EmbeddingProtocol != nil {
		model.EmbeddingProtocol = strings.TrimSpace(*req.EmbeddingProtocol)
	}
	if req.EmbeddingDimensions != nil {
		model.EmbeddingDimensions = *req.EmbeddingDimensions
	}
	if req.InputModalities != nil {
		model.InputModalities = req.InputModalities
	}
	if req.EncodingFormat != nil {
		model.EncodingFormat = strings.TrimSpace(*req.EncodingFormat)
	}
	if err := normalizeAgentModelRuntimeFields(&model); err != nil {
		return AgentModelProfile{}, err
	}
	if model.ModelType != AgentModelTypeChat {
		used, err := s.agentModelUsedByTaskProfile(ctx, model.ID)
		if err != nil {
			return AgentModelProfile{}, err
		}
		if used {
			return AgentModelProfile{}, ErrAgentModelTypeNotAllowed
		}
	}
	return s.store.UpdateAgentModelProfile(ctx, model)
}

func normalizeAgentModelRuntimeFields(model *AgentModelProfile) error {
	if model.Metadata == nil {
		model.Metadata = map[string]any{}
	}
	modelType := strings.TrimSpace(model.ModelType)
	if modelType == "" {
		modelType = stringFromAny(model.Metadata["modelType"])
	}
	normalizedType, err := normalizeAgentModelType(modelType)
	if err != nil {
		return err
	}
	model.ModelType = normalizedType
	if model.ModelType != AgentModelTypeEmbedding {
		model.EmbeddingProtocol = ""
		model.EmbeddingDimensions = 0
		model.InputModalities = nil
		model.EncodingFormat = ""
		return nil
	}
	protocol := strings.TrimSpace(model.EmbeddingProtocol)
	if protocol == "" {
		protocol = stringFromAny(model.Metadata["embeddingProtocol"])
	}
	normalizedProtocol, err := normalizeEmbeddingProtocol(protocol)
	if err != nil {
		return err
	}
	model.EmbeddingProtocol = normalizedProtocol
	if model.EmbeddingDimensions < 0 {
		return ErrInvalidAgentModelType
	}
	if model.EmbeddingDimensions == 0 {
		if value, ok := numberFromAny(model.Metadata["embeddingDimensions"]); ok && value > 0 {
			model.EmbeddingDimensions = int(value)
		}
	}
	if len(model.InputModalities) == 0 {
		model.InputModalities = agentModelStringListFromAny(model.Metadata["inputModalities"])
	}
	model.InputModalities = normalizeAgentModelInputModalities(model.InputModalities)
	if model.EmbeddingProtocol != AgentEmbeddingProtocolOpenAI {
		model.EncodingFormat = ""
		return nil
	}
	if strings.TrimSpace(model.EncodingFormat) == "" {
		model.EncodingFormat = stringFromAny(model.Metadata["encodingFormat"])
	}
	model.EncodingFormat = strings.TrimSpace(model.EncodingFormat)
	return nil
}

func normalizeAgentModelType(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return AgentModelTypeChat, nil
	}
	if !validAgentModelType(value) {
		return "", ErrInvalidAgentModelType
	}
	return value, nil
}

func normalizeEmbeddingProtocol(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return AgentEmbeddingProtocolOpenAI, nil
	}
	switch value {
	case AgentEmbeddingProtocolOpenAI, AgentEmbeddingProtocolVolcengineMultimodal:
	default:
		return "", ErrInvalidAgentModelType
	}
	return value, nil
}

func normalizeAgentModelInputModalities(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	if len(out) == 0 {
		return []string{"text"}
	}
	return out
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
	modelType, err := normalizeAgentModelType(req.ModelType)
	if err != nil {
		return AgentModelTestResult{}, err
	}
	if modelType == AgentModelTypeEmbedding {
		return s.testEmbeddingModel(ctx, profile, modelName, req)
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
		s.markAgentModelStatusForType(ctx, providerID, modelName, AgentModelTypeChat, AgentModelStatusUnavailable)
		return AgentModelTestResult{OK: false, Message: message, LatencyMS: latencyMS, ModelType: AgentModelTypeChat}, nil
	}
	defer resp.Body.Close()

	respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, openAIProbeMaxBodyBytes))
	if readErr != nil {
		message := "model test response read failed: " + safelog.Text(readErr.Error(), 600)
		s.recordAgentProviderProbe(ctx, profile, AgentProviderAvailabilityUnavailable, message)
		s.markAgentModelStatusForType(ctx, providerID, modelName, AgentModelTypeChat, AgentModelStatusUnavailable)
		return AgentModelTestResult{OK: false, Message: message, LatencyMS: latencyMS, ModelType: AgentModelTypeChat}, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := fmt.Sprintf("model test returned %s: %s", resp.Status, safelog.Text(string(respBody), 600))
		s.recordAgentProviderProbe(ctx, profile, AgentProviderAvailabilityUnavailable, message)
		s.markAgentModelStatusForType(ctx, providerID, modelName, AgentModelTypeChat, AgentModelStatusUnavailable)
		return AgentModelTestResult{OK: false, Message: message, LatencyMS: latencyMS, ModelType: AgentModelTypeChat}, nil
	}

	message := "model test ok"
	s.recordAgentProviderProbe(ctx, profile, AgentProviderAvailabilityAvailable, message)
	s.markAgentModelStatusForType(ctx, providerID, modelName, AgentModelTypeChat, AgentModelStatusAvailable)
	return AgentModelTestResult{OK: true, Message: message, LatencyMS: latencyMS, ModelType: AgentModelTypeChat}, nil
}

func (s *Service) testEmbeddingModel(ctx context.Context, profile AgentProviderProfile, modelName string, req RequestTestAgentModel) (AgentModelTestResult, error) {
	if isDefaultCodexCLIProvider(profile) {
		return AgentModelTestResult{OK: false, Message: "embedding test is not supported for the default Codex CLI provider", ModelType: AgentModelTypeEmbedding}, nil
	}
	protocol, err := normalizeEmbeddingProtocol(req.EmbeddingProtocol)
	if err != nil {
		return AgentModelTestResult{}, err
	}
	baseURL, apiKey, err := agentProviderOpenAIConfig(profile)
	if err != nil {
		return AgentModelTestResult{}, err
	}
	input := strings.TrimSpace(req.Input)
	if input == "" {
		input = "StockV2 embedding connectivity test"
	}
	body := map[string]any{}
	endpoint := "/embeddings"
	switch protocol {
	case AgentEmbeddingProtocolVolcengineMultimodal:
		// ponytail: text input verifies the multimodal endpoint; add image probes only when the UI needs image-vector tests.
		body = map[string]any{
			"model": modelName,
			"input": []map[string]string{
				{"type": "text", "text": input},
			},
		}
		endpoint = "/embeddings/multimodal"
	default:
		body = map[string]any{
			"model": modelName,
			"input": input,
		}
		if format := strings.TrimSpace(req.EncodingFormat); format != "" {
			body["encoding_format"] = format
		} else {
			body["encoding_format"] = "float"
		}
	}
	payload, _ := json.Marshal(body)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+endpoint, bytes.NewReader(payload))
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
		message := "embedding model test request failed: " + safelog.Text(err.Error(), 600)
		s.recordAgentProviderProbe(ctx, profile, AgentProviderAvailabilityUnavailable, message)
		s.markAgentModelStatusForType(ctx, profile.ID, modelName, AgentModelTypeEmbedding, AgentModelStatusUnavailable)
		return AgentModelTestResult{OK: false, Message: message, LatencyMS: latencyMS, ModelType: AgentModelTypeEmbedding}, nil
	}
	defer resp.Body.Close()

	respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, openAIProbeMaxBodyBytes))
	if readErr != nil {
		message := "embedding model test response read failed: " + safelog.Text(readErr.Error(), 600)
		s.recordAgentProviderProbe(ctx, profile, AgentProviderAvailabilityUnavailable, message)
		s.markAgentModelStatusForType(ctx, profile.ID, modelName, AgentModelTypeEmbedding, AgentModelStatusUnavailable)
		return AgentModelTestResult{OK: false, Message: message, LatencyMS: latencyMS, ModelType: AgentModelTypeEmbedding}, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := fmt.Sprintf("embedding model test returned %s: %s", resp.Status, safelog.Text(string(respBody), 600))
		s.recordAgentProviderProbe(ctx, profile, AgentProviderAvailabilityUnavailable, message)
		s.markAgentModelStatusForType(ctx, profile.ID, modelName, AgentModelTypeEmbedding, AgentModelStatusUnavailable)
		return AgentModelTestResult{OK: false, Message: message, LatencyMS: latencyMS, ModelType: AgentModelTypeEmbedding}, nil
	}

	rawEmbedding, ok := embeddingRawFromResponse(respBody)
	if !ok {
		message := "embedding model test response has no readable embedding: " + safelog.Text(string(respBody), 600)
		s.recordAgentProviderProbe(ctx, profile, AgentProviderAvailabilityDegraded, message)
		s.markAgentModelStatusForType(ctx, profile.ID, modelName, AgentModelTypeEmbedding, AgentModelStatusDegraded)
		return AgentModelTestResult{OK: false, Message: message, LatencyMS: latencyMS, ModelType: AgentModelTypeEmbedding}, nil
	}
	dimensions, ok := embeddingDimensionsFromRaw(rawEmbedding)
	if !ok {
		message := "embedding model test response is missing a readable embedding vector"
		s.recordAgentProviderProbe(ctx, profile, AgentProviderAvailabilityDegraded, message)
		s.markAgentModelStatusForType(ctx, profile.ID, modelName, AgentModelTypeEmbedding, AgentModelStatusDegraded)
		return AgentModelTestResult{OK: false, Message: message, LatencyMS: latencyMS, ModelType: AgentModelTypeEmbedding}, nil
	}
	if req.EmbeddingDimensions > 0 && req.EmbeddingDimensions != dimensions {
		message := fmt.Sprintf("embedding dimension mismatch: got %d, want %d", dimensions, req.EmbeddingDimensions)
		s.recordAgentProviderProbe(ctx, profile, AgentProviderAvailabilityDegraded, message)
		s.markAgentModelStatusForType(ctx, profile.ID, modelName, AgentModelTypeEmbedding, AgentModelStatusDegraded)
		return AgentModelTestResult{OK: false, Message: message, LatencyMS: latencyMS, ModelType: AgentModelTypeEmbedding, EmbeddingDimensions: dimensions}, nil
	}
	message := fmt.Sprintf("embedding model test ok: %d dimensions", dimensions)
	s.recordAgentProviderProbe(ctx, profile, AgentProviderAvailabilityAvailable, message)
	s.markAgentModelStatusForType(ctx, profile.ID, modelName, AgentModelTypeEmbedding, AgentModelStatusAvailable)
	return AgentModelTestResult{OK: true, Message: message, LatencyMS: latencyMS, ModelType: AgentModelTypeEmbedding, EmbeddingDimensions: dimensions}, nil
}

func embeddingRawFromResponse(respBody []byte) (json.RawMessage, bool) {
	var response struct {
		Data      json.RawMessage `json:"data"`
		Embedding json.RawMessage `json:"embedding"`
	}
	if err := json.Unmarshal(respBody, &response); err != nil {
		return nil, false
	}
	var items []struct {
		Embedding json.RawMessage `json:"embedding"`
	}
	if err := json.Unmarshal(response.Data, &items); err == nil && len(items) > 0 && len(items[0].Embedding) > 0 {
		return items[0].Embedding, true
	}
	var item struct {
		Embedding json.RawMessage `json:"embedding"`
	}
	if err := json.Unmarshal(response.Data, &item); err == nil && len(item.Embedding) > 0 {
		return item.Embedding, true
	}
	if len(response.Embedding) > 0 {
		return response.Embedding, true
	}
	return nil, false
}

func embeddingDimensionsFromRaw(raw json.RawMessage) (int, bool) {
	var floats []float64
	if err := json.Unmarshal(raw, &floats); err == nil && len(floats) > 0 {
		return len(floats), true
	}
	var nested [][]float64
	if err := json.Unmarshal(raw, &nested); err == nil && len(nested) > 0 && len(nested[0]) > 0 {
		return len(nested[0]), true
	}
	var encoded string
	if err := json.Unmarshal(raw, &encoded); err != nil || strings.TrimSpace(encoded) == "" {
		return 0, false
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(decoded) == 0 || len(decoded)%4 != 0 {
		return 0, false
	}
	return len(decoded) / 4, true
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
		s.markAgentModelStatusForType(ctx, profile.ID, modelName, AgentModelTypeChat, AgentModelStatusUnavailable)
		return AgentModelTestResult{OK: false, Message: safelog.Text(err.Error(), 700), LatencyMS: latencyMS, ModelType: AgentModelTypeChat}
	}
	for _, item := range catalog.Items {
		if item.ID == modelName {
			s.markAgentModelStatusForType(ctx, profile.ID, modelName, AgentModelTypeChat, AgentModelStatusAvailable)
			return AgentModelTestResult{OK: true, Message: "codex cli model found in catalog", LatencyMS: latencyMS, ModelType: AgentModelTypeChat}
		}
	}
	s.markAgentModelStatusForType(ctx, profile.ID, modelName, AgentModelTypeChat, AgentModelStatusUnavailable)
	return AgentModelTestResult{OK: false, Message: "model not found in codex cli catalog", LatencyMS: latencyMS, ModelType: AgentModelTypeChat}
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
			if _, err := s.ensureAgentTaskModelAllowed(ctx, primaryID); err != nil {
				return AgentTaskProfile{}, err // 防悬空绑定
			}
		}
		profile.PrimaryModelID = primaryID
	}
	if req.FallbackModelID != nil {
		fallbackID := strings.TrimSpace(*req.FallbackModelID)
		if fallbackID != "" {
			if _, err := s.ensureAgentTaskModelAllowed(ctx, fallbackID); err != nil {
				return AgentTaskProfile{}, err
			}
		}
		profile.FallbackModelID = fallbackID
	}
	if req.ReasoningEffort != nil {
		reasoningEffort := strings.ToLower(strings.TrimSpace(*req.ReasoningEffort))
		if !validAgentReasoningEffort(reasoningEffort) {
			return AgentTaskProfile{}, ErrInvalidAgentReasoningEffort
		}
		profile.ReasoningEffort = reasoningEffort
	}
	return s.store.UpdateAgentTaskProfile(ctx, profile)
}

func (s *Service) ensureAgentTaskModelAllowed(ctx context.Context, modelID string) (AgentModelProfile, error) {
	model, err := s.store.GetAgentModelProfile(ctx, modelID)
	if err != nil {
		return AgentModelProfile{}, err
	}
	if model.ModelType != AgentModelTypeChat {
		return AgentModelProfile{}, ErrAgentModelTypeNotAllowed
	}
	return model, nil
}

func (s *Service) agentModelUsedByTaskProfile(ctx context.Context, modelID string) (bool, error) {
	profiles, err := s.store.ListAgentTaskProfiles(ctx, AgentTaskProfileListFilter{Limit: 1000})
	if err != nil {
		return false, err
	}
	for _, profile := range profiles {
		if profile.PrimaryModelID == modelID || profile.FallbackModelID == modelID {
			return true, nil
		}
	}
	return false, nil
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
	if run.TaskType == AgentTaskTypeStrategyGeneration {
		steps, err := s.store.ListStrategyGenerationSteps(ctx, run.ID)
		if err != nil {
			return AgentExecutionDetail{}, err
		}
		contexts, err := s.store.ListStrategyGenerationContextItems(ctx, run.ID)
		if err != nil {
			return AgentExecutionDetail{}, err
		}
		detail.StrategyGenerationSteps = steps
		detail.StrategyGenerationContexts = contexts
	}
	if run.TaskType == AgentTaskTypePortfolioSentinel && run.TriggerObjectType == "portfolio_sentinel_run" && run.TriggerObjectID != "" {
		if sentinelDetail, err := s.GetPortfolioSentinelRunDetail(ctx, run.TriggerObjectID); err == nil {
			detail.Metadata = map[string]any{"portfolioSentinel": sentinelDetail}
		} else if !errors.Is(err, ErrPortfolioSentinelRunNotFound) {
			return AgentExecutionDetail{}, err
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
	if _, err := s.ensureAgentTaskModelAllowed(ctx, params.ModelID); err != nil {
		return AgentRun{}, AgentDecisionLedger{}, err
	}
	params.ReasoningEffort = strings.ToLower(strings.TrimSpace(params.ReasoningEffort))
	if !validAgentReasoningEffort(params.ReasoningEffort) {
		return AgentRun{}, AgentDecisionLedger{}, ErrInvalidAgentReasoningEffort
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
		ReasoningEffort:   params.ReasoningEffort,
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
		ReasoningEffort:   taskProfile.ReasoningEffort,
		TriggerObjectType: triggerObjectType,
		TriggerObjectID:   triggerObjectID,
		RequestedBy:       requestedBy,
	}

	run, ledger, err := s.CreateAgentRunRecord(ctx, AgentRunRecordParams{
		TaskType:          taskType,
		ProviderID:        model.ProviderID,
		ModelID:           model.ID,
		ReasoningEffort:   taskProfile.ReasoningEffort,
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
		if m.Enabled && m.Status == AgentModelStatusAvailable && m.ModelType == AgentModelTypeChat {
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
	s.markAgentModelStatusForType(ctx, providerID, modelName, "", status)
}

func (s *Service) markAgentModelStatusForType(ctx context.Context, providerID, modelName, modelType, status string) {
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
		if modelType != "" && model.ModelType != modelType {
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
	if model.ModelType != AgentModelTypeChat {
		return AgentExecutionDetail{}, ErrAgentModelTypeNotAllowed
	}
	if !model.Enabled || model.Status != AgentModelStatusAvailable {
		return AgentExecutionDetail{}, ErrAgentModelNotAvailable
	}
	if strings.TrimSpace(req.DebugMode) == AgentTaskTypeOpportunityDiscovery {
		return s.runOpportunityDiscoveryCLIDebug(ctx, req, model)
	}

	triggerID := "debug-" + generateID()
	today := time.Now().Format("2006-01-02")
	pack := AgentContextPack{
		BuiltAt: time.Now(),
		Hit: MonitorHit{
			ID:       triggerID,
			TaskType: "agent_cli_debug",
			Status:   MonitorHitStatusCandidate,
			Title:    "Agent CLI debug self check with Google News search",
			Summary:  "Verify Codex CLI execution, search/web tool access, stdout capture, and MCP submit_result callback.",
			Evidence: map[string]any{
				"debug":                    true,
				"expectedOutputType":       OperationReviewOutputContinueMonitoring,
				"googleNewsDate":           today,
				"googleNewsSearchRequired": true,
				"requiredLanguage":         "zh-CN",
				"requiredResultField":      "googleNewsTodayZh",
				"instruction":              "Use available search/web tools to find today's Google News headlines, then return continue_monitoring in Chinese through stock_agent.submit_result.",
			},
		},
		Evidence: map[string]any{
			"debug":                    true,
			"googleNewsDate":           today,
			"googleNewsSearchRequired": true,
			"requiredLanguage":         "zh-CN",
			"requiredResultField":      "googleNewsTodayZh",
		},
		Freshness: map[string]any{
			"generatedAt":    time.Now().Format(time.RFC3339),
			"purpose":        "agent_cli_debug",
			"googleNewsDate": today,
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
		InputSummary:         "CLI debug: verify Codex CLI search/web access with today's Google News in Chinese and MCP submit_result callback.",
		InputArtifactSummary: string(inputArtifact),
	})
	if err != nil {
		return AgentExecutionDetail{}, err
	}
	if req.Async {
		go s.startAgentRunAsync(context.Background(), run, ledger, pack, model.ModelName)
		return s.GetAgentExecutionDetail(ctx, run.ID)
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
				s.log.Error("agent run panicked", "run_id", run.ID, "task_type", run.TaskType, "trigger_object_type", run.TriggerObjectType, "trigger_object_id", run.TriggerObjectID, "model", modelName, "panic", r)
			}
			s.finalizeAgentRun(ctx, run.ID, nil, fmt.Errorf("panic: %v", r))
		}
	}()

	if s.agentExecutor == nil {
		s.finalizeAgentRun(ctx, run.ID, nil, fmt.Errorf("no executor configured"))
		return
	}

	if _, _, err := s.executeAgentRun(ctx, run, ledger, pack, modelName); err != nil && s.log != nil {
		s.log.Warn("agent run finished with error", "run_id", run.ID, "ledger_id", ledger.ID, "task_type", run.TaskType, "trigger_object_type", run.TriggerObjectType, "trigger_object_id", run.TriggerObjectID, "model", modelName, "error", safelog.Text(err.Error(), 300))
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
			s.log.Warn("update agent run to running failed", "run_id", run.ID, "task_type", run.TaskType, "trigger_object_type", run.TriggerObjectType, "trigger_object_id", run.TriggerObjectID, "error", safelog.Text(err.Error(), 240))
		}
	}

	taskID, _ := s.agentTaskPool.createTask(run.TaskType, run.ID, "", 10*time.Minute)

	execOutput, execErr := s.agentExecutor.ExecuteOperationReview(ctx, taskID, pack, modelName, run.ReasoningEffort)

	s.finalizeAgentRunWithOutput(ctx, run.ID, ledger.ID, taskID, execOutput, execErr)
	finalRun, finalLedger := s.safeGetAgentRunAndLedger(ctx, run.ID, ledger.ID)
	return finalRun, finalLedger, execErr
}

func (s *Service) startStrategyGenerationRunAsync(
	ctx context.Context,
	run AgentRun,
	ledger AgentDecisionLedger,
	genCtx StrategyGenerationContext,
	modelName string,
) {
	defer func() {
		if r := recover(); r != nil {
			if s.log != nil {
				s.log.Error("strategy generation agent run panicked", "run_id", run.ID, "ledger_id", ledger.ID, "task_type", run.TaskType, "trigger_object_type", run.TriggerObjectType, "trigger_object_id", run.TriggerObjectID, "model", modelName, "panic", r)
			}
			s.finalizeAgentRun(ctx, run.ID, nil, fmt.Errorf("panic: %v", r))
		}
	}()

	if s.agentExecutor == nil {
		s.finalizeAgentRun(ctx, run.ID, nil, fmt.Errorf("no executor configured"))
		return
	}
	if _, _, err := s.executeStrategyGenerationRun(ctx, run, ledger, genCtx, modelName); err != nil && s.log != nil {
		s.log.Warn("strategy generation agent run finished with error", "run_id", run.ID, "ledger_id", ledger.ID, "task_type", run.TaskType, "trigger_object_type", run.TriggerObjectType, "trigger_object_id", run.TriggerObjectID, "model", modelName, "error", safelog.Text(err.Error(), 300))
	}
}

func (s *Service) executeStrategyGenerationRun(
	ctx context.Context,
	run AgentRun,
	ledger AgentDecisionLedger,
	genCtx StrategyGenerationContext,
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
			s.log.Warn("update strategy generation run to running failed", "run_id", run.ID, "task_type", run.TaskType, "trigger_object_type", run.TriggerObjectType, "trigger_object_id", run.TriggerObjectID, "error", safelog.Text(err.Error(), 240))
		}
	}

	taskID, execOutput, execErr := s.executeStrategyGenerationPipeline(ctx, run, genCtx, modelName)
	s.finalizeAgentRunWithOutput(ctx, run.ID, ledger.ID, taskID, execOutput, execErr)
	finalRun, finalLedger := s.safeGetAgentRunAndLedger(ctx, run.ID, ledger.ID)
	return finalRun, finalLedger, execErr
}

func (s *Service) executeStrategyGenerationPipeline(
	ctx context.Context,
	run AgentRun,
	genCtx StrategyGenerationContext,
	modelName string,
) (string, *AgentExecutorOutput, error) {
	_ = s.addStrategyGenerationContext(ctx, run.ID, "", "context_bundle", "StrategyGenerationContext", map[string]any{"context": genCtx}, "", 1)
	prior := map[string]any{}
	var finalTaskID string
	var finalOutput *AgentExecutorOutput
	steps := strategyGenerationPipelineSteps()
	for idx, def := range steps {
		step, err := s.store.CreateStrategyGenerationStep(ctx, StrategyGenerationStepRun{
			RunID:        run.ID,
			StepKey:      def.key,
			StepName:     def.name,
			Role:         def.role,
			Status:       StrategyGenerationStepStatusPending,
			SequenceNo:   idx + 1,
			InputSummary: def.objective,
		})
		if err != nil && s.log != nil {
			s.log.Warn("create strategy generation step failed", "run_id", run.ID, "step", def.key, "error", safelog.Text(err.Error(), 240))
		}
		step.Status = StrategyGenerationStepStatusRunning
		step.StartedAt = time.Now()
		_, _ = s.store.UpdateStrategyGenerationStep(ctx, step)

		pack := StrategyGenerationStepPack{
			RunID:        run.ID,
			StepKey:      def.key,
			Role:         def.role,
			Objective:    def.objective,
			Instructions: def.instructions,
			Context:      genCtx,
			PriorResults: prior,
		}
		execOutput, execErr, taskID := s.executeStrategyGenerationStepWithRetry(ctx, run, step, pack, modelName)
		step.Prompt = ""
		if execOutput != nil {
			step.Prompt = safelog.Text(execOutput.Prompt, 16384)
			step.OutputArtifactSummary = safelog.Text(agentExecutorOutputSummary(execOutput), 16384)
		}
		submitted := s.consumeAgentTaskSubmittedResult(taskID)
		if execErr != nil && submitted == nil {
			step.Status = StrategyGenerationStepStatusFailed
			step.ErrorMessage = agentRunFailureMessage(execErr.Error(), execOutput)
			step.FinishedAt = time.Now()
			_, _ = s.store.UpdateStrategyGenerationStep(ctx, step)
			return "", execOutput, execErr
		}
		if submitted == nil || submitted.OutputType != StrategyGenerationOutputType {
			step.Status = StrategyGenerationStepStatusFailed
			step.ErrorMessage = "no valid strategy_generation step result submitted"
			step.FinishedAt = time.Now()
			_, _ = s.store.UpdateStrategyGenerationStep(ctx, step)
			return "", execOutput, errors.New(step.ErrorMessage)
		}

		step.Status = StrategyGenerationStepStatusCompleted
		step.OutputSummary = safelog.Text(submitted.ResultSummary, 2000)
		step.StructuredOutput = map[string]any{
			"outputType":    submitted.OutputType,
			"resultSummary": submitted.ResultSummary,
			"confidence":    submitted.Confidence,
			"result":        submitted.Result,
		}
		step.FinishedAt = time.Now()
		_, _ = s.store.UpdateStrategyGenerationStep(ctx, step)
		_ = s.addStrategyGenerationContext(ctx, run.ID, step.ID, "step_result", def.name, submitted.Result, submitted.ResultSummary, idx+2)

		prior[def.key] = submitted.Result
		if def.key == StrategyGenerationStepFormatter {
			finalTaskID = taskID
			finalOutput = execOutput
			s.restoreAgentTaskSubmittedResult(finalTaskID, run.TaskType, run.ID, submitted)
		}
	}
	return finalTaskID, finalOutput, nil
}

func (s *Service) executeStrategyGenerationStepWithRetry(
	ctx context.Context,
	run AgentRun,
	step StrategyGenerationStepRun,
	pack StrategyGenerationStepPack,
	modelName string,
) (*AgentExecutorOutput, error, string) {
	const maxAttempts = 2
	var lastOutput *AgentExecutorOutput
	var lastErr error
	var taskID string
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		taskID, _ = s.agentTaskPool.createTask(run.TaskType, run.ID, "", 10*time.Minute)
		output, err := s.agentExecutor.ExecuteStrategyGenerationStep(ctx, taskID, pack, modelName, run.ReasoningEffort)
		lastOutput = output
		lastErr = err
		if err == nil || !retryableNoSubmitTimeout(err, output) || attempt == maxAttempts {
			return output, err, taskID
		}
		if s.log != nil {
			s.log.Warn("retrying strategy generation step after timeout without submission", "run_id", run.ID, "step_id", step.ID, "step_key", step.StepKey, "attempt", attempt, "max_attempts", maxAttempts, "duration", outputDurationString(output), "error", safelog.Text(err.Error(), 240))
		}
	}
	return lastOutput, lastErr, taskID
}

func retryableNoSubmitTimeout(err error, output *AgentExecutorOutput) bool {
	if err == nil || output == nil || !output.TimedOut {
		return false
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "timed out") && strings.Contains(text, "no result submitted")
}

func outputDurationString(output *AgentExecutorOutput) string {
	if output == nil {
		return ""
	}
	return output.Duration.String()
}

type strategyGenerationPipelineStepDef struct {
	key          string
	name         string
	role         string
	objective    string
	instructions []string
}

func strategyGenerationPipelineSteps() []strategyGenerationPipelineStepDef {
	return []strategyGenerationPipelineStepDef{
		{
			key:       StrategyGenerationStepEvidenceCollector,
			name:      "证据收集",
			role:      StrategyGenerationStepEvidenceCollector,
			objective: "Collect a compact fact pack for the targets or portfolio holdings.",
			instructions: []string{
				"Call project MCP tools and Codex CLI external public search/browse as equal-priority evidence channels to fill quote, daily bars, profile, news, existing strategy, portfolio, embedding status, and recent public context.",
				"Use serenity-skill methodology for material holdings/themes: map value-chain layers, scarce constraints, evidence strength, and failure conditions, while keeping this step's JSON schema.",
				"When internal data is stale, missing, or contradictory, verify with external public sources and output conflict_resolution plus research_log; do not only recommend future verification.",
				"Do not produce strategy recommendations.",
				"Mark stale, missing, or weak evidence explicitly.",
			},
		},
		{
			key:       StrategyGenerationStepBullResearcher,
			name:      "多头研究",
			role:      StrategyGenerationStepBullResearcher,
			objective: "Build the bullish or constructive case for each target or holding.",
			instructions: []string{
				"Use the evidence collector output and context, and refresh with internal MCP or external public search when a material constructive claim needs confirmation.",
				"Use serenity-skill to distinguish scarce-layer exposure from generic theme exposure before making constructive claims.",
				"If the evidence collector marked a material conflict as unresolved, verify it before using the claim or explicitly downgrade it.",
				"Focus on reasons to observe, hold, build_position, or add_position.",
				"Every important claim should point to evidence_refs or state that support is weak.",
			},
		},
		{
			key:       StrategyGenerationStepBearResearcher,
			name:      "空头研究",
			role:      StrategyGenerationStepBearResearcher,
			objective: "Build the bearish or risk case for each target or holding.",
			instructions: []string{
				"Use the evidence collector output and context, and refresh with internal MCP or external public search when a material risk claim needs confirmation.",
				"Use serenity-skill failure-condition thinking: substitution, faster competitor expansion, weak demand, margin failure, financing pressure, governance, geopolitics, customer loss, and valuation already pricing in success.",
				"If stale or conflicting data drives the risk case, verify it with internal MCP and public sources or label it unresolved with confidence impact.",
				"Focus on reasons to reduce_position, exit_position, avoid adding, or require Review.",
				"Every important claim should point to evidence_refs or state that support is weak.",
			},
		},
		{
			key:       StrategyGenerationStepEvidenceChecker,
			name:      "证据校验",
			role:      StrategyGenerationStepEvidenceChecker,
			objective: "Check bull and bear claims against the fact pack.",
			instructions: []string{
				"Do not introduce new investment opinions.",
				"Use internal MCP and external public search as equal-priority verification channels when checking contested, stale, or high-impact claims.",
				"Grade evidence using serenity-skill source standards: strong primary filings/announcements/contracts/project records/patents; medium reputable media/trade/company pages/cross-checks; weak social or rumor leads.",
				"For every contested quote/bar/profile/news/portfolio claim, output verified, partially_verified, weak, unsupported, stale, or conflicting with the verification source and unresolved reason.",
				"Label claims as verified, partially_verified, weak, unsupported, stale, or conflicting.",
				"Identify claims that must not be used by the final formatter.",
			},
		},
		{
			key:       StrategyGenerationStepPortfolioJudge,
			name:      "组合裁决",
			role:      StrategyGenerationStepPortfolioJudge,
			objective: "Apply portfolio constraints and decide the desired draft type and action space.",
			instructions: []string{
				"Respect cash, concentration, risk level, existing strategy coverage, and allowed actions.",
				"Refresh portfolio, strategy coverage, and material market context with internal MCP and external public search when prior outputs are stale or conflicting.",
				"Use serenity-skill scarce-layer and evidence-strength conclusions only as research inputs; portfolio permissions and StockV2 guardrails decide allowed action space.",
				"Do not base draft type or action-space decisions on unresolved conflicting data without explicitly stating the degraded assumption.",
				"Decide new_strategy, strategy_patch, or no_change for each holding or target.",
				"Use review_request when immediate account-bound handling is needed; do not create proposed_operation.",
			},
		},
		{
			key:       StrategyGenerationStepFormatter,
			name:      "策略草案结构化",
			role:      StrategyGenerationStepFormatter,
			objective: "Format the prior pipeline outputs into the final strategy-generation-report/v1.",
			instructions: []string{
				"Do not add new claims beyond prior results.",
				"Carry forward unresolved conflicts, research_log summaries, and data_quality_notes into the final report instead of dropping them.",
				"Carry forward serenity-skill scarce-layer/evidence-strength/failure-condition summaries when prior steps produced them.",
				"Generate playbook.rules[] using the StockV2 protocol.",
				"Use empty arrays for unstructured prefilters.",
			},
		},
	}
}

func (s *Service) consumeAgentTaskSubmittedResult(taskID string) *AgentTaskSubmittedResult {
	if taskID == "" {
		return nil
	}
	var submitted *AgentTaskSubmittedResult
	if entry, ok := s.agentTaskPool.getTask(taskID); ok {
		entry.mu.Lock()
		submitted = entry.submittedResult
		entry.mu.Unlock()
	}
	s.agentTaskPool.remove(taskID)
	return submitted
}

func (s *Service) restoreAgentTaskSubmittedResult(taskID, taskType, runID string, submitted *AgentTaskSubmittedResult) {
	if taskID == "" || submitted == nil {
		return
	}
	entry := &agentTaskEntry{
		id:              taskID,
		taskType:        taskType,
		agentRunID:      runID,
		deadline:        time.Now().Add(10 * time.Minute),
		status:          agentTaskStatusSubmitted,
		submittedResult: submitted,
		submittedAt:     time.Now(),
		submitCount:     1,
		resultCh:        make(chan struct{}),
	}
	close(entry.resultCh)
	s.agentTaskPool.mu.Lock()
	s.agentTaskPool.tasks[taskID] = entry
	s.agentTaskPool.mu.Unlock()
}

func (s *Service) addStrategyGenerationContext(ctx context.Context, runID, stepID, contextType, title string, content map[string]any, text string, sequence int) error {
	_, err := s.store.AddStrategyGenerationContextItem(ctx, StrategyGenerationContextItem{
		RunID:       runID,
		StepID:      stepID,
		ContextType: contextType,
		Title:       title,
		ContentJSON: content,
		ContentText: safelog.Text(text, 4000),
		SequenceNo:  sequence,
	})
	if err != nil && s.log != nil {
		s.log.Warn("add strategy generation context failed", "run_id", runID, "step_id", stepID, "context_type", contextType, "error", safelog.Text(err.Error(), 240))
	}
	return err
}

func agentExecutorOutputSummary(output *AgentExecutorOutput) string {
	if output == nil {
		return ""
	}
	var b strings.Builder
	if output.Command != "" {
		b.WriteString("command:\n")
		b.WriteString(output.Command)
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "exit_code: %d\n", output.ExitCode)
	fmt.Fprintf(&b, "duration: %s\n", output.Duration)
	if output.TimedOut {
		b.WriteString("timed_out: true\n")
	}
	if output.StdoutTail != "" {
		b.WriteString("stdout_tail:\n")
		b.WriteString(output.StdoutTail)
	}
	if output.StderrTail != "" {
		b.WriteString("stderr_tail:\n")
		b.WriteString(output.StderrTail)
	}
	return b.String()
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
			s.log.Error("finalize: get run failed", "run_id", runID, "error", safelog.Text(err.Error(), 240))
		}
		return
	}

	if ledgerID == "" {
		ledgerID = run.DecisionLedgerID
	}
	ledger, err := s.store.GetAgentDecisionLedger(ctx, ledgerID)
	if err != nil {
		if s.log != nil {
			s.log.Warn("finalize: get ledger failed", "run_id", runID, "ledger_id", ledgerID, "task_type", run.TaskType, "trigger_object_type", run.TriggerObjectType, "trigger_object_id", run.TriggerObjectID, "error", safelog.Text(err.Error(), 240))
		}
	}

	// 准备 output artifact summary
	var outputArtifact strings.Builder
	if execOutput != nil {
		if strings.TrimSpace(execOutput.Prompt) != "" {
			if run.TaskType == AgentTaskTypeNewsEventReview && run.TriggerObjectType == "news_context_run" {
				// ponytail: the durable theme/version/evidence objects are the audit
				// artifact. Retaining the full batch prompt would copy news bodies back
				// into SQLite after the source asset passes safe compaction.
				ledger.Prompt = "消息脉络归纳使用固定系统约束执行；批次新闻正文不长期保留，完整覆盖结果见结构化输出。"
				if ledger.RedactionSummary == nil {
					ledger.RedactionSummary = map[string]any{}
				}
				ledger.RedactionSummary["promptCompacted"] = true
			} else {
				ledger.Prompt = safelog.Text(execOutput.Prompt, 16384)
			}
			if ledger.RedactionSummary == nil {
				ledger.RedactionSummary = map[string]any{}
			}
			ledger.RedactionSummary["promptRedacted"] = agentRedacted(execOutput.Prompt)
		}
		if execOutput.Command != "" {
			outputArtifact.WriteString("command:\n")
			outputArtifact.WriteString(execOutput.Command)
			outputArtifact.WriteString("\n")
		}
		fmt.Fprintf(&outputArtifact, "exit_code: %d\n", execOutput.ExitCode)
		fmt.Fprintf(&outputArtifact, "duration: %s\n", execOutput.Duration)
		if execOutput.TimedOut {
			outputArtifact.WriteString("timed_out: true\n")
		}
		includeEmptyTails := execErr != nil || execOutput.ExitCode != 0 || execOutput.TimedOut
		retainTails := run.TaskType != AgentTaskTypeNewsEventReview || run.TriggerObjectType != "news_context_run" || includeEmptyTails
		if retainTails && execOutput.StdoutTail != "" {
			outputArtifact.WriteString("stdout_tail:\n")
			outputArtifact.WriteString(execOutput.StdoutTail)
		} else if includeEmptyTails {
			outputArtifact.WriteString("stdout_tail: (empty)\n")
		}
		if retainTails && execOutput.StderrTail != "" {
			outputArtifact.WriteString("stderr_tail:\n")
			outputArtifact.WriteString(execOutput.StderrTail)
		} else if includeEmptyTails {
			outputArtifact.WriteString("stderr_tail: (empty)\n")
		}
	}

	// 判断成功还是失败
	hasValidResult := submitted != nil && submitted.OutputType != "" &&
		validAgentTaskOutputType(run.TaskType, submitted.OutputType)

	if execErr != nil && !hasValidResult {
		// 失败路径
		run.Status = AgentRunStatusFailed
		run.ErrorMessage = agentRunFailureMessage(execErr.Error(), execOutput)
		now := time.Now()
		run.FinishedAt = now
		s.markStockProfileAIEnhancementFailed(ctx, run, run.ErrorMessage)
		s.markOpportunityDiscoveryRunFailed(ctx, run, run.ErrorMessage)
		s.markPortfolioSentinelAgentRunFailed(ctx, run, run.ErrorMessage)

		ledger.OutputArtifactSummary = safelog.Text(outputArtifact.String(), 16384)
		if s.log != nil {
			s.log.Warn("agent run finalized as failed", "run_id", run.ID, "ledger_id", ledger.ID, "task_type", run.TaskType, "trigger_object_type", run.TriggerObjectType, "trigger_object_id", run.TriggerObjectID, "status", run.Status, "error", safelog.Text(run.ErrorMessage, 300))
		}

		if _, err := s.store.UpdateAgentRun(ctx, run); err != nil && s.log != nil {
			s.log.Warn("finalize: update run failed", "run_id", runID, "task_type", run.TaskType, "error", safelog.Text(err.Error(), 240))
		}
		if _, err := s.store.UpdateAgentDecisionLedger(ctx, ledger); err != nil && s.log != nil {
			s.log.Warn("finalize: update ledger failed", "run_id", runID, "ledger_id", ledger.ID, "task_type", run.TaskType, "error", safelog.Text(err.Error(), 240))
		}
		return
	}

	// 成功路径
	if !hasValidResult {
		// 进程退出了但没提交 result,也算失败
		run.Status = AgentRunStatusFailed
		run.ErrorMessage = agentRunFailureMessage("no valid result submitted", execOutput)
		run.FinishedAt = time.Now()
		s.markStockProfileAIEnhancementFailed(ctx, run, run.ErrorMessage)
		s.markOpportunityDiscoveryRunFailed(ctx, run, run.ErrorMessage)
		s.markPortfolioSentinelAgentRunFailed(ctx, run, run.ErrorMessage)
		ledger.OutputArtifactSummary = safelog.Text(outputArtifact.String(), 16384)
		if s.log != nil {
			s.log.Warn("agent run finalized without valid result", "run_id", run.ID, "ledger_id", ledger.ID, "task_type", run.TaskType, "trigger_object_type", run.TriggerObjectType, "trigger_object_id", run.TriggerObjectID, "status", run.Status, "error", safelog.Text(run.ErrorMessage, 300))
		}
		if _, err := s.store.UpdateAgentRun(ctx, run); err != nil && s.log != nil {
			s.log.Warn("finalize: update run after missing result failed", "run_id", runID, "task_type", run.TaskType, "error", safelog.Text(err.Error(), 240))
		}
		if _, err := s.store.UpdateAgentDecisionLedger(ctx, ledger); err != nil && s.log != nil {
			s.log.Warn("finalize: update ledger after missing result failed", "run_id", runID, "ledger_id", ledger.ID, "task_type", run.TaskType, "error", safelog.Text(err.Error(), 240))
		}
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

	// 更新 Review result(自动跑 guardrails)。先让闭环对象落地成功，再把 AgentRun 暴露为 completed。
	if run.TaskType == AgentTaskTypeOperationReview && run.TriggerObjectType == "operation_review" && run.TriggerObjectID != "" {
		reviewID := run.TriggerObjectID
		saveReq := RequestSaveOperationReviewResult{
			OutputType:    submitted.OutputType,
			Result:        submitted.Result,
			ResultSummary: submitted.ResultSummary,
			Status:        OperationReviewStatusCompleted,
		}
		if _, err := s.saveOperationReviewResult(ctx, reviewID, saveReq, &run); err != nil {
			run.Status = AgentRunStatusFailed
			run.ErrorMessage = safelog.Text("save review result failed: "+err.Error(), 500)
			if _, updateErr := s.store.UpdateAgentRun(ctx, run); updateErr != nil && s.log != nil {
				s.log.Warn("finalize: update run after review save failed", "run_id", runID, "ledger_id", ledger.ID, "task_type", run.TaskType, "trigger_object_type", run.TriggerObjectType, "trigger_object_id", run.TriggerObjectID, "error", safelog.Text(updateErr.Error(), 240))
			}
			if s.log != nil {
				s.log.Warn("finalize: save review result failed", "run_id", runID, "ledger_id", ledger.ID, "task_type", run.TaskType, "review_id", reviewID, "error", safelog.Text(err.Error(), 300))
			}
			if _, ledgerErr := s.store.UpdateAgentDecisionLedger(ctx, ledger); ledgerErr != nil && s.log != nil {
				s.log.Warn("finalize: update ledger after review save failed", "run_id", runID, "ledger_id", ledger.ID, "task_type", run.TaskType, "review_id", reviewID, "error", safelog.Text(ledgerErr.Error(), 240))
			}
			return
		}
	}
	if run.TaskType == AgentTaskTypeStockProfileSummary && run.TriggerObjectType == "stock_profile" && run.TriggerObjectID != "" {
		modelName := s.agentRunModelName(ctx, run)
		if _, err := s.applyStockProfileEnhancementResult(ctx, run.TriggerObjectID, submitted.Result, modelName, submitted.Confidence); err != nil {
			run.Status = AgentRunStatusFailed
			run.ErrorMessage = safelog.Text("save stock profile enhancement failed: "+err.Error(), 500)
			s.markStockProfileUpdateTaskAIResult(ctx, run.ID, StockProfileUpdateStatusPartial, StockProfileAIStatusFailed, run.ErrorMessage)
			if _, updateErr := s.store.UpdateAgentRun(ctx, run); updateErr != nil && s.log != nil {
				s.log.Warn("finalize: update run after stock profile save failed", "run_id", runID, "ledger_id", ledger.ID, "task_type", run.TaskType, "symbol", run.TriggerObjectID, "model", modelName, "error", safelog.Text(updateErr.Error(), 240))
			}
			if s.log != nil {
				s.log.Warn("finalize: save stock profile enhancement failed", "run_id", runID, "ledger_id", ledger.ID, "task_type", run.TaskType, "symbol", run.TriggerObjectID, "model", modelName, "error", safelog.Text(err.Error(), 300))
			}
			if _, ledgerErr := s.store.UpdateAgentDecisionLedger(ctx, ledger); ledgerErr != nil && s.log != nil {
				s.log.Warn("finalize: update ledger after stock profile save failed", "run_id", runID, "ledger_id", ledger.ID, "task_type", run.TaskType, "symbol", run.TriggerObjectID, "model", modelName, "error", safelog.Text(ledgerErr.Error(), 240))
			}
			return
		}
		s.markStockProfileUpdateTaskAIResult(ctx, run.ID, StockProfileUpdateStatusCompleted, StockProfileAIStatusReady, "")
	}
	if run.TaskType == AgentTaskTypeStrategyGeneration {
		report, err := strategyGenerationReportFromResult(submitted.Result)
		if err != nil {
			run.Status = AgentRunStatusFailed
			run.ErrorMessage = safelog.Text("invalid strategy generation result: "+err.Error(), 500)
			if s.log != nil {
				s.log.Warn("finalize: invalid strategy generation result", "run_id", runID, "ledger_id", ledger.ID, "task_type", run.TaskType, "trigger_object_type", run.TriggerObjectType, "trigger_object_id", run.TriggerObjectID, "error", safelog.Text(err.Error(), 300))
			}
			if _, updateErr := s.store.UpdateAgentRun(ctx, run); updateErr != nil && s.log != nil {
				s.log.Warn("finalize: update run after strategy generation validation failed", "run_id", runID, "ledger_id", ledger.ID, "task_type", run.TaskType, "trigger_object_type", run.TriggerObjectType, "trigger_object_id", run.TriggerObjectID, "error", safelog.Text(updateErr.Error(), 240))
			}
			if _, ledgerErr := s.store.UpdateAgentDecisionLedger(ctx, ledger); ledgerErr != nil && s.log != nil {
				s.log.Warn("finalize: update ledger after strategy generation validation failed", "run_id", runID, "ledger_id", ledger.ID, "task_type", run.TaskType, "trigger_object_type", run.TriggerObjectType, "trigger_object_id", run.TriggerObjectID, "error", safelog.Text(ledgerErr.Error(), 240))
			}
			return
		}
		created, err := s.createDraftStrategiesFromStrategyGeneration(ctx, run, *submitted, report)
		if err != nil {
			run.Status = AgentRunStatusFailed
			run.ErrorMessage = safelog.Text("save strategy generation draft failed: "+strategyGenerationSaveError(err).Error(), 500)
			if _, updateErr := s.store.UpdateAgentRun(ctx, run); updateErr != nil && s.log != nil {
				s.log.Warn("finalize: update run after strategy generation save failed", "run_id", runID, "ledger_id", ledger.ID, "task_type", run.TaskType, "trigger_object_type", run.TriggerObjectType, "trigger_object_id", run.TriggerObjectID, "error", safelog.Text(updateErr.Error(), 240))
			}
			if s.log != nil {
				s.log.Warn("finalize: save strategy generation draft failed", "run_id", runID, "ledger_id", ledger.ID, "task_type", run.TaskType, "trigger_object_type", run.TriggerObjectType, "trigger_object_id", run.TriggerObjectID, "error", safelog.Text(strategyGenerationSaveError(err).Error(), 300))
			}
			if _, ledgerErr := s.store.UpdateAgentDecisionLedger(ctx, ledger); ledgerErr != nil && s.log != nil {
				s.log.Warn("finalize: update ledger after strategy generation save failed", "run_id", runID, "ledger_id", ledger.ID, "task_type", run.TaskType, "trigger_object_type", run.TriggerObjectType, "trigger_object_id", run.TriggerObjectID, "error", safelog.Text(ledgerErr.Error(), 240))
			}
			return
		}
		createdSummaries := make([]map[string]string, 0, len(created))
		for _, item := range created {
			createdSummaries = append(createdSummaries, map[string]string{
				"id":     item.Strategy.ID,
				"symbol": item.Strategy.Symbol,
				"status": item.Strategy.Status,
			})
		}
		ledger.StructuredOutput["createdStrategies"] = createdSummaries
	}
	if run.TaskType == AgentTaskTypeOpportunityDiscovery {
		discoveryRun, err := s.store.GetOpportunityDiscoveryRunByAgentRunID(ctx, run.ID)
		if err != nil {
			run.Status = AgentRunStatusFailed
			run.ErrorMessage = safelog.Text("opportunity discovery run not found: "+err.Error(), 500)
			if s.log != nil {
				s.log.Warn("finalize: opportunity discovery run lookup failed", "run_id", runID, "ledger_id", ledger.ID, "task_type", run.TaskType, "trigger_object_type", run.TriggerObjectType, "trigger_object_id", run.TriggerObjectID, "error", safelog.Text(err.Error(), 300))
			}
			if _, updateErr := s.store.UpdateAgentRun(ctx, run); updateErr != nil && s.log != nil {
				s.log.Warn("finalize: update run after opportunity discovery lookup failed", "run_id", runID, "ledger_id", ledger.ID, "task_type", run.TaskType, "trigger_object_type", run.TriggerObjectType, "trigger_object_id", run.TriggerObjectID, "error", safelog.Text(updateErr.Error(), 240))
			}
			return
		}
		result, err := s.ProcessOpportunityDiscoverySubmittedResult(ctx, discoveryRun.ID, *submitted)
		if err != nil {
			run.Status = AgentRunStatusFailed
			run.ErrorMessage = safelog.Text("save opportunity discovery result failed: "+err.Error(), 500)
			if s.log != nil {
				s.log.Warn("finalize: save opportunity discovery result failed", "run_id", runID, "ledger_id", ledger.ID, "task_type", run.TaskType, "discovery_run_id", discoveryRun.ID, "opportunity_id", discoveryRun.OpportunityID, "error", safelog.Text(err.Error(), 300))
			}
			if _, updateErr := s.store.UpdateAgentRun(ctx, run); updateErr != nil && s.log != nil {
				s.log.Warn("finalize: update run after opportunity discovery save failed", "run_id", runID, "ledger_id", ledger.ID, "task_type", run.TaskType, "discovery_run_id", discoveryRun.ID, "opportunity_id", discoveryRun.OpportunityID, "error", safelog.Text(updateErr.Error(), 240))
			}
			if _, ledgerErr := s.store.UpdateAgentDecisionLedger(ctx, ledger); ledgerErr != nil && s.log != nil {
				s.log.Warn("finalize: update ledger after opportunity discovery save failed", "run_id", runID, "ledger_id", ledger.ID, "task_type", run.TaskType, "discovery_run_id", discoveryRun.ID, "opportunity_id", discoveryRun.OpportunityID, "error", safelog.Text(ledgerErr.Error(), 240))
			}
			return
		}
		ledger.StructuredOutput["opportunityResultId"] = result.ID
	}
	if run.TaskType == AgentTaskTypeNewsEventReview && run.TriggerObjectType == "news_context_run" && run.TriggerObjectID != "" {
		result, err := s.ProcessNewsContextSubmittedResult(ctx, run.TriggerObjectID, run.ID, *submitted)
		if err != nil {
			run.Status = AgentRunStatusFailed
			run.ErrorMessage = safelog.Text("save news context result failed: "+err.Error(), 500)
			if s.log != nil {
				s.log.Warn("finalize: save news context result failed", "run_id", runID, "ledger_id", ledger.ID, "news_context_run_id", run.TriggerObjectID, "error", safelog.Text(err.Error(), 300))
			}
			if _, updateErr := s.store.UpdateAgentRun(ctx, run); updateErr != nil && s.log != nil {
				s.log.Warn("finalize: update run after news context save failed", "run_id", runID, "error", safelog.Text(updateErr.Error(), 240))
			}
			if _, ledgerErr := s.store.UpdateAgentDecisionLedger(ctx, ledger); ledgerErr != nil && s.log != nil {
				s.log.Warn("finalize: update ledger after news context save failed", "run_id", runID, "ledger_id", ledger.ID, "error", safelog.Text(ledgerErr.Error(), 240))
			}
			return
		}
		ledger.StructuredOutput["newsContextApplyResult"] = result
	}
	if run.TaskType == AgentTaskTypePortfolioSentinel && run.TriggerObjectType == "portfolio_sentinel_run" && run.TriggerObjectID != "" {
		result, err := s.ProcessPortfolioSentinelSubmittedResult(ctx, run.TriggerObjectID, *submitted)
		if err != nil {
			run.Status = AgentRunStatusFailed
			run.ErrorMessage = safelog.Text("save portfolio sentinel result failed: "+err.Error(), 500)
			if s.log != nil {
				s.log.Warn("finalize: save portfolio sentinel result failed", "run_id", runID, "ledger_id", ledger.ID, "task_type", run.TaskType, "sentinel_run_id", run.TriggerObjectID, "error", safelog.Text(err.Error(), 300))
			}
			if _, updateErr := s.store.UpdateAgentRun(ctx, run); updateErr != nil && s.log != nil {
				s.log.Warn("finalize: update run after portfolio sentinel save failed", "run_id", runID, "ledger_id", ledger.ID, "task_type", run.TaskType, "sentinel_run_id", run.TriggerObjectID, "error", safelog.Text(updateErr.Error(), 240))
			}
			if _, ledgerErr := s.store.UpdateAgentDecisionLedger(ctx, ledger); ledgerErr != nil && s.log != nil {
				s.log.Warn("finalize: update ledger after portfolio sentinel save failed", "run_id", runID, "ledger_id", ledger.ID, "task_type", run.TaskType, "sentinel_run_id", run.TriggerObjectID, "error", safelog.Text(ledgerErr.Error(), 240))
			}
			return
		}
		ledger.StructuredOutput["portfolioSentinelResultId"] = result.ID
	}

	if _, err := s.store.UpdateAgentDecisionLedger(ctx, ledger); err != nil && s.log != nil {
		s.log.Warn("finalize: update ledger failed", "run_id", runID, "ledger_id", ledger.ID, "task_type", run.TaskType, "trigger_object_type", run.TriggerObjectType, "trigger_object_id", run.TriggerObjectID, "error", safelog.Text(err.Error(), 240))
	}
	if _, err := s.store.UpdateAgentRun(ctx, run); err != nil && s.log != nil {
		s.log.Warn("finalize: update run failed", "run_id", runID, "ledger_id", ledger.ID, "task_type", run.TaskType, "trigger_object_type", run.TriggerObjectType, "trigger_object_id", run.TriggerObjectID, "error", safelog.Text(err.Error(), 240))
	}
}

func agentRunFailureMessage(base string, execOutput *AgentExecutorOutput) string {
	message := strings.TrimSpace(base)
	if message == "" {
		message = "agent run failed"
	}
	if execOutput == nil {
		return safelog.Text(message, 500)
	}
	if providerMessage := agentProviderFailureMessage(execOutput.StdoutTail); providerMessage != "" {
		// ponytail: Codex --json reports provider failures on stdout. Prefer that
		// bounded event message over incidental CLI stderr such as stdin notices.
		return safelog.Text(message+": "+providerMessage, 500)
	}
	if strings.TrimSpace(execOutput.StderrTail) == "" {
		return safelog.Text(message, 500)
	}
	stderr := lastNonEmptyLine(execOutput.StderrTail)
	if stderr == "" {
		return safelog.Text(message, 500)
	}
	// ponytail: Keep the run error compact; full stdout/stderr remains in the ledger detail.
	return safelog.Text(message+": "+stderr, 500)
}

func agentProviderFailureMessage(stdout string) string {
	lines := strings.Split(strings.ReplaceAll(stdout, "\r\n", "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		var event struct {
			Type    string `json:"type"`
			Message string `json:"message"`
			Error   struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal([]byte(strings.TrimSpace(lines[i])), &event) != nil {
			continue
		}
		if event.Type != "error" && event.Type != "turn.failed" {
			continue
		}
		if message := strings.TrimSpace(firstNonEmpty(event.Error.Message, event.Message)); message != "" {
			return safelog.Text(message, 500)
		}
	}
	return ""
}

func lastNonEmptyLine(text string) string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return line
		}
	}
	return ""
}

func (s *Service) agentRunModelName(ctx context.Context, run AgentRun) string {
	model, err := s.store.GetAgentModelProfile(ctx, run.ModelID)
	if err == nil && strings.TrimSpace(model.ModelName) != "" {
		return model.ModelName
	}
	return strings.TrimSpace(run.ModelID)
}

// validOperationReviewOutputType 在 review_service.go 中定义
