package stockv2

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	"phantom-lancer/internal/safelog"
)

type embeddingAssetSource struct {
	ObjectType string
	ObjectID   string
	Text       string
}

type SemanticSearchHit struct {
	Asset     EmbeddingAsset `json:"asset"`
	Score     float64        `json:"score"`
	Profile   *StockProfile  `json:"profile,omitempty"`
	NewsEvent *NewsEvent     `json:"newsEvent,omitempty"`
}

func (s *Service) GetEmbeddingStatus(ctx context.Context) (EmbeddingStatus, error) {
	cfg, err := s.embeddingConfigOrDefault(ctx)
	if err != nil {
		return EmbeddingStatus{}, err
	}
	status := EmbeddingStatus{Config: cfg, Status: EmbeddingStatusModelNotConfigured, Code: EmbeddingStatusModelNotConfigured}
	if strings.TrimSpace(cfg.EmbeddingModelID) == "" {
		status.Message = "embedding model is not configured"
		return status, nil
	}
	if !cfg.Enabled {
		status.Status = EmbeddingStatusDisabled
		status.Code = EmbeddingStatusDisabled
		status.Message = "embedding is disabled"
		return status, nil
	}
	model, err := s.store.GetAgentModelProfile(ctx, cfg.EmbeddingModelID)
	if err != nil {
		status.Status = EmbeddingStatusModelUnavailable
		status.Code = EmbeddingStatusModelUnavailable
		status.Message = "embedding model not found"
		return status, nil
	}
	status.Model = &model
	ready, stale, failed, _ := s.store.CountEmbeddingAssetsByStatus(ctx, model.ID)
	status.ReadyAssets = ready
	status.StaleAssets = stale
	status.FailedAssets = failed
	if err := validateEmbeddingModel(model); err != nil {
		status.Status = EmbeddingStatusModelUnavailable
		status.Code = EmbeddingStatusModelUnavailable
		status.Message = err.Error()
		return status, nil
	}
	if ready == 0 {
		status.Status = EmbeddingStatusAssetNotReady
		status.Code = EmbeddingStatusAssetNotReady
		status.Message = "embedding assets are empty or stale; rebuild first"
		return status, nil
	}
	status.Ready = true
	status.Status = EmbeddingStatusReady
	status.Code = ""
	status.Message = "embedding is ready"
	return status, nil
}

func (s *Service) UpdateEmbeddingConfig(ctx context.Context, req RequestUpdateEmbeddingConfig) (EmbeddingStatus, error) {
	cfg, err := s.embeddingConfigOrDefault(ctx)
	if err != nil {
		return EmbeddingStatus{}, err
	}
	if req.EmbeddingModelID != nil {
		modelID := strings.TrimSpace(*req.EmbeddingModelID)
		if modelID != "" {
			model, err := s.store.GetAgentModelProfile(ctx, modelID)
			if err != nil {
				return EmbeddingStatus{}, err
			}
			if model.ModelType != AgentModelTypeEmbedding {
				return EmbeddingStatus{}, ErrEmbeddingModelInvalid
			}
		}
		cfg.EmbeddingModelID = modelID
	}
	if req.Enabled != nil {
		cfg.Enabled = *req.Enabled
	}
	cfg.LastProbeStatus = EmbeddingStatusModelNotConfigured
	cfg.LastError = ""
	if cfg.Enabled && cfg.EmbeddingModelID != "" {
		model, err := s.store.GetAgentModelProfile(ctx, cfg.EmbeddingModelID)
		if err != nil {
			return EmbeddingStatus{}, err
		}
		if err := validateEmbeddingModel(model); err != nil {
			return EmbeddingStatus{}, err
		}
		cfg.LastProbeStatus = EmbeddingStatusReady
	}
	if _, err := s.store.UpsertEmbeddingConfig(ctx, cfg); err != nil {
		return EmbeddingStatus{}, err
	}
	return s.GetEmbeddingStatus(ctx)
}

func (s *Service) RebuildEmbeddingAssets(ctx context.Context, req RequestRebuildEmbeddingAssets) (EmbeddingRebuildResult, error) {
	model, cfg, err := s.ensureEmbeddingModelReady(ctx)
	if err != nil {
		return EmbeddingRebuildResult{}, err
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 200 {
		limit = 200
	}
	objectTypes := normalizeEmbeddingObjectTypes(req.ObjectTypes)
	sources, err := s.collectEmbeddingSources(ctx, objectTypes, limit)
	if err != nil {
		return EmbeddingRebuildResult{}, err
	}
	result := EmbeddingRebuildResult{Status: "completed", Total: len(sources)}
	for _, source := range sources {
		text := strings.TrimSpace(source.Text)
		if text == "" {
			result.Skipped++
			continue
		}
		textHash := hashEmbeddingText(text)
		if !req.Force {
			if existing, err := s.store.GetEmbeddingAssetByObject(ctx, source.ObjectType, source.ObjectID, model.ID); err == nil &&
				existing.Status == EmbeddingAssetStatusReady &&
				existing.TextHash == textHash &&
				existing.EmbeddingDimensions == model.EmbeddingDimensions {
				result.Skipped++
				continue
			}
		}
		vector, err := s.generateEmbedding(ctx, model, text)
		if err != nil {
			result.Failed++
			result.FailedItems = append(result.FailedItems, UpdateFailure{Symbol: source.ObjectID, Reason: safelog.Text(err.Error(), 240)})
			_, _ = s.store.UpsertEmbeddingAsset(ctx, EmbeddingAsset{
				ObjectType:          source.ObjectType,
				ObjectID:            source.ObjectID,
				TextHash:            textHash,
				TextSummary:         safelog.Text(text, 500),
				ModelID:             model.ID,
				ProviderID:          model.ProviderID,
				EmbeddingProtocol:   model.EmbeddingProtocol,
				EmbeddingDimensions: model.EmbeddingDimensions,
				Status:              EmbeddingAssetStatusFailed,
				ErrorMessage:        safelog.Text(err.Error(), 500),
			})
			continue
		}
		if err := validateEmbeddingDimensions(model, len(vector)); err != nil {
			result.Failed++
			result.FailedItems = append(result.FailedItems, UpdateFailure{Symbol: source.ObjectID, Reason: err.Error()})
			continue
		}
		asset := EmbeddingAsset{
			ObjectType:          source.ObjectType,
			ObjectID:            source.ObjectID,
			TextHash:            textHash,
			TextSummary:         safelog.Text(text, 500),
			ModelID:             model.ID,
			ProviderID:          model.ProviderID,
			EmbeddingProtocol:   model.EmbeddingProtocol,
			EmbeddingDimensions: len(vector),
			VectorRef:           "emb_" + generateID(),
			Status:              EmbeddingAssetStatusReady,
		}
		if existing, err := s.store.GetEmbeddingAssetByObject(ctx, source.ObjectType, source.ObjectID, model.ID); err == nil && existing.VectorRef != "" {
			asset.ID = existing.ID
			asset.VectorRef = existing.VectorRef
		}
		if err := s.store.UpsertEmbeddingVector(ctx, asset, vector); err != nil {
			result.Failed++
			result.FailedItems = append(result.FailedItems, UpdateFailure{Symbol: source.ObjectID, Reason: safelog.Text(err.Error(), 240)})
			continue
		}
		if _, err := s.store.UpsertEmbeddingAsset(ctx, asset); err != nil {
			result.Failed++
			result.FailedItems = append(result.FailedItems, UpdateFailure{Symbol: source.ObjectID, Reason: safelog.Text(err.Error(), 240)})
			continue
		}
		result.Succeeded++
	}
	if result.Failed > 0 && result.Succeeded == 0 {
		result.Status = "failed"
	} else if result.Failed > 0 {
		result.Status = "partial"
	}
	cfg.LastProbeAt = time.Now()
	cfg.LastProbeStatus = result.Status
	cfg.LastError = ""
	if result.Failed > 0 {
		cfg.LastError = fmt.Sprintf("%d embedding assets failed", result.Failed)
	}
	_, _ = s.store.UpsertEmbeddingConfig(ctx, cfg)
	return result, nil
}

func (s *Service) ListEmbeddingAssets(ctx context.Context, filter EmbeddingAssetListFilter) ([]EmbeddingAsset, error) {
	filter.Limit = normalizedOpportunityLimit(filter.Limit)
	filter.Offset = normalizedOpportunityOffset(filter.Offset)
	return s.store.ListEmbeddingAssets(ctx, filter)
}

func (s *Service) CountEmbeddingAssets(ctx context.Context, filter EmbeddingAssetListFilter) (int, error) {
	return s.store.CountEmbeddingAssets(ctx, filter)
}

func (s *Service) SemanticSearch(ctx context.Context, objectType, query string, limit int) ([]SemanticSearchHit, error) {
	model, _, err := s.ensureEmbeddingModelReady(ctx)
	if err != nil {
		return nil, err
	}
	ready, _, _, err := s.store.CountEmbeddingAssetsByStatus(ctx, model.ID)
	if err != nil {
		return nil, err
	}
	if ready == 0 {
		return nil, ErrEmbeddingAssetNotReady
	}
	vector, err := s.generateEmbedding(ctx, model, query)
	if err != nil {
		return nil, err
	}
	if err := validateEmbeddingDimensions(model, len(vector)); err != nil {
		return nil, err
	}
	hits, err := s.store.SearchEmbeddingVectors(ctx, model.ID, objectType, vector, limit)
	if err != nil {
		return nil, err
	}
	out := make([]SemanticSearchHit, 0, len(hits))
	for _, hit := range hits {
		asset, err := s.store.GetEmbeddingAssetByVectorRef(ctx, hit.VectorRef)
		if err != nil || asset.Status != EmbeddingAssetStatusReady {
			continue
		}
		item := SemanticSearchHit{Asset: asset, Score: hit.Score}
		switch asset.ObjectType {
		case EmbeddingObjectStockProfile:
			if profile, err := s.store.GetStockProfile(ctx, asset.ObjectID); err == nil {
				item.Profile = &profile
			}
		case EmbeddingObjectNewsEvent:
			if event, err := s.store.GetNewsEvent(ctx, asset.ObjectID); err == nil {
				item.NewsEvent = &event
			}
		}
		out = append(out, item)
	}
	return out, nil
}

func (s *Service) embeddingConfigOrDefault(ctx context.Context) (EmbeddingConfig, error) {
	cfg, err := s.store.GetEmbeddingConfig(ctx)
	if err == nil {
		return cfg, nil
	}
	if !errors.Is(err, ErrEmbeddingConfigNotFound) {
		return EmbeddingConfig{}, err
	}
	return s.store.UpsertEmbeddingConfig(ctx, EmbeddingConfig{
		ID:              EmbeddingConfigIDDefault,
		Enabled:         false,
		LastProbeStatus: EmbeddingStatusModelNotConfigured,
	})
}

func (s *Service) ensureEmbeddingModelReady(ctx context.Context) (AgentModelProfile, EmbeddingConfig, error) {
	cfg, err := s.embeddingConfigOrDefault(ctx)
	if err != nil {
		return AgentModelProfile{}, EmbeddingConfig{}, err
	}
	if strings.TrimSpace(cfg.EmbeddingModelID) == "" {
		return AgentModelProfile{}, cfg, ErrEmbeddingModelNotConfigured
	}
	if !cfg.Enabled {
		return AgentModelProfile{}, cfg, ErrEmbeddingDisabled
	}
	model, err := s.store.GetAgentModelProfile(ctx, cfg.EmbeddingModelID)
	if err != nil {
		return AgentModelProfile{}, cfg, err
	}
	if err := validateEmbeddingModel(model); err != nil {
		return AgentModelProfile{}, cfg, err
	}
	return model, cfg, nil
}

func validateEmbeddingModel(model AgentModelProfile) error {
	if model.ModelType != AgentModelTypeEmbedding {
		return ErrEmbeddingModelInvalid
	}
	if !model.Enabled || model.Status != AgentModelStatusAvailable {
		return ErrEmbeddingModelUnavailable
	}
	if model.EmbeddingDimensions <= 0 {
		return ErrEmbeddingDimensionsMismatch
	}
	return nil
}

func validateEmbeddingDimensions(model AgentModelProfile, got int) error {
	if model.EmbeddingDimensions > 0 && got != model.EmbeddingDimensions {
		return ErrEmbeddingDimensionsMismatch
	}
	if got <= 0 {
		return ErrEmbeddingDimensionsMismatch
	}
	return nil
}

func normalizeEmbeddingObjectTypes(values []string) []string {
	if len(values) == 0 {
		return []string{EmbeddingObjectStockProfile, EmbeddingObjectNewsEvent, EmbeddingObjectOpportunity}
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		switch value {
		case EmbeddingObjectStockProfile, EmbeddingObjectNewsEvent, EmbeddingObjectOpportunity:
			if !seen[value] {
				seen[value] = true
				out = append(out, value)
			}
		}
	}
	return out
}

func (s *Service) collectEmbeddingSources(ctx context.Context, objectTypes []string, limit int) ([]embeddingAssetSource, error) {
	out := make([]embeddingAssetSource, 0)
	for _, objectType := range objectTypes {
		switch objectType {
		case EmbeddingObjectStockProfile:
			items, err := s.store.ListStockProfiles(ctx, StockProfileListFilter{Limit: limit})
			if err != nil {
				return nil, err
			}
			for _, item := range items {
				out = append(out, embeddingAssetSource{ObjectType: objectType, ObjectID: item.Symbol, Text: stockProfileEmbeddingText(item)})
			}
		case EmbeddingObjectNewsEvent:
			items, err := s.store.ListNewsEvents(ctx, NewsEventListFilter{Limit: limit})
			if err != nil {
				return nil, err
			}
			for _, item := range items {
				out = append(out, embeddingAssetSource{ObjectType: objectType, ObjectID: item.ID, Text: newsEventEmbeddingText(item)})
			}
		case EmbeddingObjectOpportunity:
			items, err := s.store.ListOpportunities(ctx, OpportunityListFilter{Limit: limit})
			if err != nil {
				return nil, err
			}
			for _, item := range items {
				out = append(out, embeddingAssetSource{ObjectType: objectType, ObjectID: item.ID, Text: item.Title + "\n" + item.UserThesis})
			}
		}
	}
	return out, nil
}

func stockProfileEmbeddingText(item StockProfile) string {
	parts := []string{item.Symbol, item.Name, item.Industry, item.BusinessSummary, item.BusinessSummaryZh, item.BusinessSummaryEn, item.ProfileText, item.ProfileTextZh, item.ProfileTextEn}
	parts = append(parts, item.Concepts...)
	parts = append(parts, item.Tags...)
	parts = append(parts, item.KeywordsZh...)
	parts = append(parts, item.KeywordsEn...)
	return strings.Join(compactStrings(parts), "\n")
}

func newsEventEmbeddingText(item NewsEvent) string {
	return strings.Join(compactStrings([]string{item.Title, item.Summary, item.Content, item.Source}), "\n")
}

func hashEmbeddingText(text string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(text)))
	return hex.EncodeToString(sum[:])
}

func (s *Service) generateEmbedding(ctx context.Context, model AgentModelProfile, input string) ([]float64, error) {
	provider, err := s.store.GetAgentProviderProfile(ctx, model.ProviderID)
	if err != nil {
		return nil, err
	}
	if isDefaultCodexCLIProvider(provider) {
		return nil, ErrEmbeddingModelUnavailable
	}
	baseURL, apiKey, err := agentProviderOpenAIConfig(provider)
	if err != nil {
		return nil, err
	}
	body := map[string]any{}
	endpoint := "/embeddings"
	switch model.EmbeddingProtocol {
	case AgentEmbeddingProtocolVolcengineMultimodal:
		body = map[string]any{
			"model": model.ModelName,
			"input": []map[string]string{{"type": "text", "text": safelog.Text(input, 12000)}},
		}
		endpoint = "/embeddings/multimodal"
	default:
		body = map[string]any{
			"model":           model.ModelName,
			"input":           safelog.Text(input, 12000),
			"encoding_format": firstNonEmptyOpportunity(model.EncodingFormat, "float"),
		}
	}
	payload, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := s.agentHTTPClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("embedding request failed: %s", safelog.Text(err.Error(), 600))
	}
	defer resp.Body.Close()
	respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, openAIProbeMaxBodyBytes))
	if readErr != nil {
		return nil, fmt.Errorf("embedding response read failed: %s", safelog.Text(readErr.Error(), 600))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("embedding returned %s: %s", resp.Status, safelog.Text(string(respBody), 600))
	}
	raw, ok := embeddingRawFromResponse(respBody)
	if !ok {
		return nil, fmt.Errorf("embedding response has no readable vector")
	}
	vector, ok := embeddingVectorFromRaw(raw)
	if !ok {
		return nil, fmt.Errorf("embedding response vector is not readable")
	}
	return vector, nil
}

func embeddingVectorFromRaw(raw json.RawMessage) ([]float64, bool) {
	var floats []float64
	if err := json.Unmarshal(raw, &floats); err == nil && len(floats) > 0 {
		return floats, true
	}
	var nested [][]float64
	if err := json.Unmarshal(raw, &nested); err == nil && len(nested) > 0 && len(nested[0]) > 0 {
		return nested[0], true
	}
	var encoded string
	if err := json.Unmarshal(raw, &encoded); err != nil || strings.TrimSpace(encoded) == "" {
		return nil, false
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(decoded) == 0 || len(decoded)%4 != 0 {
		return nil, false
	}
	out := make([]float64, 0, len(decoded)/4)
	for i := 0; i < len(decoded); i += 4 {
		bits := binary.LittleEndian.Uint32(decoded[i : i+4])
		out = append(out, float64(math.Float32frombits(bits)))
	}
	return out, len(out) > 0
}
