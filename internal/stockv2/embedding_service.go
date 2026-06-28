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
	"sort"
	"strings"
	"time"

	"phantom-lancer/internal/safelog"
)

func (s *Service) GetEmbeddingStatus(ctx context.Context) (EmbeddingStatus, error) {
	config, err := s.store.GetEmbeddingConfig(ctx)
	if err != nil {
		return EmbeddingStatus{}, err
	}
	status := EmbeddingStatus{Config: config}
	ready, _ := s.store.CountEmbeddingAssetsByStatus(ctx, EmbeddingAssetStatusReady)
	stale, _ := s.store.CountEmbeddingAssetsByStatus(ctx, EmbeddingAssetStatusStale)
	failed, _ := s.store.CountEmbeddingAssetsByStatus(ctx, EmbeddingAssetStatusFailed)
	status.ReadyAssetCount = ready
	status.StaleAssetCount = stale
	status.FailedAssetCount = failed

	binding, err := s.resolveEmbeddingModel(ctx)
	if err != nil {
		status.ErrorCode = embeddingErrorCode(err)
		status.ErrorMessage = err.Error()
		return status, nil
	}
	status.Available = true
	status.Config = binding.Config
	status.ModelID = binding.Model.ID
	status.ProviderID = binding.Model.ProviderID
	status.ModelName = binding.Model.ModelName
	status.EmbeddingProtocol = binding.Model.EmbeddingProtocol
	status.EmbeddingDimensions = binding.Model.EmbeddingDimensions
	return status, nil
}

func (s *Service) UpdateEmbeddingConfig(ctx context.Context, req RequestUpdateEmbeddingConfig) (EmbeddingStatus, error) {
	config, err := s.store.GetEmbeddingConfig(ctx)
	if err != nil {
		return EmbeddingStatus{}, err
	}
	oldModelID := strings.TrimSpace(config.EmbeddingModelID)
	if req.EmbeddingModelID != nil {
		config.EmbeddingModelID = strings.TrimSpace(*req.EmbeddingModelID)
	}
	if req.Enabled != nil {
		config.Enabled = *req.Enabled
	}
	if strings.TrimSpace(config.EmbeddingModelID) == "" {
		config.Enabled = false
	}
	if config.Enabled {
		model, err := s.store.GetAgentModelProfile(ctx, config.EmbeddingModelID)
		if err != nil || model.ModelType != AgentModelTypeEmbedding {
			return EmbeddingStatus{}, ErrEmbeddingModelNotConfigured
		}
		if !model.Enabled || model.Status != AgentModelStatusAvailable {
			return EmbeddingStatus{}, ErrEmbeddingModelUnavailable
		}
		config.LastProbeStatus = AgentModelStatusAvailable
		config.LastError = ""
		config.LastProbeAt = time.Now()
	}
	updated, err := s.store.UpdateEmbeddingConfig(ctx, config)
	if err != nil {
		return EmbeddingStatus{}, err
	}
	if oldModelID != "" && oldModelID != strings.TrimSpace(updated.EmbeddingModelID) {
		if err := s.store.MarkEmbeddingAssetsStaleForModelChange(ctx, updated.EmbeddingModelID); err != nil {
			return EmbeddingStatus{}, err
		}
	}
	return s.GetEmbeddingStatus(ctx)
}

func (s *Service) RebuildEmbeddingAssets(ctx context.Context, req RequestRebuildEmbeddingAssets) (EmbeddingRebuildResult, error) {
	binding, err := s.resolveEmbeddingModel(ctx)
	if err != nil {
		return EmbeddingRebuildResult{}, err
	}
	objectTypes := normalizeEmbeddingObjectTypes(req.ObjectTypes)
	result := EmbeddingRebuildResult{ObjectTypes: objectTypes, UpdatedAt: time.Now()}
	limit := req.Limit
	for _, objectType := range objectTypes {
		switch objectType {
		case EmbeddingObjectStockProfile:
			s.rebuildStockProfileEmbeddings(ctx, binding, limit, &result)
		case EmbeddingObjectNewsEvent:
			s.rebuildNewsEventEmbeddings(ctx, binding, limit, &result)
		}
	}
	return result, nil
}

func (s *Service) SemanticSearchStockProfiles(ctx context.Context, req SemanticSearchRequest) ([]SemanticStockProfileResult, error) {
	queryVector, binding, err := s.embedSemanticSearchQuery(ctx, req.Query)
	if err != nil {
		return nil, err
	}
	assets, scores, err := s.semanticSearchAssets(ctx, EmbeddingObjectStockProfile, binding, queryVector, req.Limit, req.MinScore)
	if err != nil {
		return nil, err
	}
	out := make([]SemanticStockProfileResult, 0, len(assets))
	for _, asset := range assets {
		profile, err := s.store.GetStockProfile(ctx, asset.ObjectID)
		if err != nil {
			continue
		}
		out = append(out, SemanticStockProfileResult{
			Score:   scores[asset.ID],
			Profile: profile,
			Asset:   asset,
		})
	}
	return out, nil
}

func (s *Service) SemanticSearchNewsEvents(ctx context.Context, req SemanticSearchRequest) ([]SemanticNewsEventResult, error) {
	queryVector, binding, err := s.embedSemanticSearchQuery(ctx, req.Query)
	if err != nil {
		return nil, err
	}
	assets, scores, err := s.semanticSearchAssets(ctx, EmbeddingObjectNewsEvent, binding, queryVector, req.Limit, req.MinScore)
	if err != nil {
		return nil, err
	}
	out := make([]SemanticNewsEventResult, 0, len(assets))
	for _, asset := range assets {
		event, err := s.store.GetNewsEvent(ctx, asset.ObjectID)
		if err != nil {
			continue
		}
		out = append(out, SemanticNewsEventResult{
			Score: scores[asset.ID],
			Event: event,
			Asset: asset,
		})
	}
	return out, nil
}

func (s *Service) ListEmbeddingAssets(ctx context.Context, filter EmbeddingAssetListFilter) ([]EmbeddingAsset, error) {
	return s.store.ListEmbeddingAssets(ctx, filter)
}

func (s *Service) CountEmbeddingAssets(ctx context.Context, filter EmbeddingAssetListFilter) (int, error) {
	return s.store.CountEmbeddingAssets(ctx, filter)
}

func (s *Service) resolveEmbeddingModel(ctx context.Context) (embeddingModelBinding, error) {
	config, err := s.store.GetEmbeddingConfig(ctx)
	if err != nil {
		return embeddingModelBinding{}, err
	}
	if !config.Enabled || strings.TrimSpace(config.EmbeddingModelID) == "" {
		return embeddingModelBinding{}, ErrEmbeddingModelNotConfigured
	}
	if status := strings.TrimSpace(config.LastProbeStatus); status != "" && status != AgentModelStatusAvailable {
		return embeddingModelBinding{}, ErrEmbeddingModelUnavailable
	}
	model, err := s.store.GetAgentModelProfile(ctx, config.EmbeddingModelID)
	if err != nil {
		if errors.Is(err, ErrAgentModelNotFound) {
			return embeddingModelBinding{}, ErrEmbeddingModelNotConfigured
		}
		return embeddingModelBinding{}, err
	}
	if model.ModelType != AgentModelTypeEmbedding {
		return embeddingModelBinding{}, ErrEmbeddingModelNotConfigured
	}
	if !model.Enabled || model.Status != AgentModelStatusAvailable {
		return embeddingModelBinding{}, ErrEmbeddingModelUnavailable
	}
	provider, err := s.store.GetAgentProviderProfile(ctx, model.ProviderID)
	if err != nil {
		return embeddingModelBinding{}, ErrEmbeddingModelUnavailable
	}
	if _, _, err := agentProviderOpenAIConfig(provider); err != nil {
		return embeddingModelBinding{}, ErrEmbeddingModelUnavailable
	}
	return embeddingModelBinding{Config: config, Model: model, Provider: provider}, nil
}

func (s *Service) embedSemanticSearchQuery(ctx context.Context, query string) ([]float64, embeddingModelBinding, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, embeddingModelBinding{}, ErrInvalidEmbeddingRequest
	}
	binding, err := s.resolveEmbeddingModel(ctx)
	if err != nil {
		return nil, embeddingModelBinding{}, err
	}
	vector, err := s.generateTextEmbedding(ctx, binding, query)
	if err != nil {
		return nil, embeddingModelBinding{}, err
	}
	return vector, binding, nil
}

func (s *Service) semanticSearchAssets(ctx context.Context, objectType string, binding embeddingModelBinding, queryVector []float64, limit int, minScore float64) ([]EmbeddingAsset, map[string]float64, error) {
	if limit <= 0 {
		limit = 10
	}
	if limit > 50 {
		limit = 50
	}
	dimensions := len(queryVector)
	// ponytail: exact scan over 1000 ready vectors is enough for the current personal universe; switch this to DuckDB VSS or paged scans when recall assets grow past that.
	assets, err := s.store.ListReadyEmbeddingAssetsForSearch(ctx, objectType, binding.Model.ID, dimensions, 1000)
	if err != nil {
		return nil, nil, err
	}
	if len(assets) == 0 {
		return nil, nil, ErrEmbeddingAssetNotReady
	}
	refs := make([]string, 0, len(assets))
	for _, asset := range assets {
		refs = append(refs, asset.VectorRef)
	}
	vectors, err := s.store.GetEmbeddingVectors(ctx, refs)
	if err != nil {
		return nil, nil, err
	}
	type scoredAsset struct {
		asset EmbeddingAsset
		score float64
	}
	scored := make([]scoredAsset, 0, len(assets))
	for _, asset := range assets {
		vector := vectors[asset.VectorRef]
		if len(vector) != dimensions {
			continue
		}
		score := cosineSimilarity(queryVector, vector)
		if minScore != 0 && score < minScore {
			continue
		}
		scored = append(scored, scoredAsset{asset: asset, score: score})
	}
	if len(scored) == 0 {
		return nil, nil, ErrEmbeddingAssetNotReady
	}
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].score == scored[j].score {
			return scored[i].asset.ObjectID < scored[j].asset.ObjectID
		}
		return scored[i].score > scored[j].score
	})
	if len(scored) > limit {
		scored = scored[:limit]
	}
	out := make([]EmbeddingAsset, 0, len(scored))
	scores := make(map[string]float64, len(scored))
	for _, item := range scored {
		out = append(out, item.asset)
		scores[item.asset.ID] = item.score
	}
	return out, scores, nil
}

func (s *Service) rebuildStockProfileEmbeddings(ctx context.Context, binding embeddingModelBinding, limit int, result *EmbeddingRebuildResult) {
	remaining := normalizeEmbeddingRebuildLimit(limit)
	offset := 0
	for remaining > 0 {
		pageLimit := minInt(200, remaining)
		profiles, err := s.store.ListStockProfiles(ctx, StockProfileListFilter{Limit: pageLimit, Offset: offset})
		if err != nil {
			result.Failed++
			result.FailedItems = append(result.FailedItems, UpdateFailure{Symbol: EmbeddingObjectStockProfile, Reason: err.Error()})
			return
		}
		if len(profiles) == 0 {
			return
		}
		for _, profile := range profiles {
			result.Total++
			if err := s.rebuildOneEmbeddingAsset(ctx, binding, EmbeddingObjectStockProfile, profile.Symbol, stockProfileEmbeddingText(profile)); err != nil {
				result.Failed++
				result.FailedItems = append(result.FailedItems, UpdateFailure{Symbol: profile.Symbol, Reason: safelog.Text(err.Error(), 240)})
				continue
			}
			result.Success++
		}
		offset += len(profiles)
		remaining -= len(profiles)
		if len(profiles) < pageLimit {
			return
		}
	}
}

func (s *Service) rebuildNewsEventEmbeddings(ctx context.Context, binding embeddingModelBinding, limit int, result *EmbeddingRebuildResult) {
	remaining := normalizeEmbeddingRebuildLimit(limit)
	offset := 0
	for remaining > 0 {
		pageLimit := minInt(200, remaining)
		events, err := s.store.ListNewsEvents(ctx, NewsEventListFilter{Limit: pageLimit, Offset: offset})
		if err != nil {
			result.Failed++
			result.FailedItems = append(result.FailedItems, UpdateFailure{Symbol: EmbeddingObjectNewsEvent, Reason: err.Error()})
			return
		}
		if len(events) == 0 {
			return
		}
		for _, event := range events {
			result.Total++
			if err := s.rebuildOneEmbeddingAsset(ctx, binding, EmbeddingObjectNewsEvent, event.ID, newsEventEmbeddingText(event)); err != nil {
				result.Failed++
				result.FailedItems = append(result.FailedItems, UpdateFailure{Symbol: event.ID, Reason: safelog.Text(err.Error(), 240)})
				continue
			}
			result.Success++
		}
		offset += len(events)
		remaining -= len(events)
		if len(events) < pageLimit {
			return
		}
	}
}

func (s *Service) rebuildOneEmbeddingAsset(ctx context.Context, binding embeddingModelBinding, objectType, objectID, text string) error {
	text = trimEmbeddingInput(text)
	if strings.TrimSpace(text) == "" {
		return ErrInvalidEmbeddingRequest
	}
	vector, err := s.generateTextEmbedding(ctx, binding, text)
	if err != nil {
		asset := EmbeddingAsset{
			ObjectType:          objectType,
			ObjectID:            objectID,
			TextHash:            embeddingTextHash(text),
			TextSummary:         safelog.Text(text, 500),
			ModelID:             binding.Model.ID,
			ProviderID:          binding.Model.ProviderID,
			EmbeddingProtocol:   binding.Model.EmbeddingProtocol,
			EmbeddingDimensions: maxInt(binding.Model.EmbeddingDimensions, len(vector)),
			Status:              EmbeddingAssetStatusFailed,
			ErrorMessage:        safelog.Text(err.Error(), 500),
		}
		_, _ = s.store.UpsertEmbeddingAsset(ctx, asset)
		return err
	}
	dimensions := len(vector)
	asset := EmbeddingAsset{
		ObjectType:          objectType,
		ObjectID:            objectID,
		TextHash:            embeddingTextHash(text),
		TextSummary:         safelog.Text(text, 500),
		ModelID:             binding.Model.ID,
		ProviderID:          binding.Model.ProviderID,
		EmbeddingProtocol:   binding.Model.EmbeddingProtocol,
		EmbeddingDimensions: dimensions,
		VectorRef:           generateID(),
		Status:              EmbeddingAssetStatusReady,
	}
	if err := s.store.UpsertEmbeddingVector(ctx, asset.VectorRef, binding.Model.ID, dimensions, vector); err != nil {
		return err
	}
	if err := s.store.MarkEmbeddingObjectAssetsStaleExcept(ctx, objectType, objectID, binding.Model.ID, dimensions); err != nil {
		return err
	}
	_, err = s.store.UpsertEmbeddingAsset(ctx, asset)
	return err
}

func (s *Service) generateTextEmbedding(ctx context.Context, binding embeddingModelBinding, text string) ([]float64, error) {
	baseURL, apiKey, err := agentProviderOpenAIConfig(binding.Provider)
	if err != nil {
		return nil, ErrEmbeddingModelUnavailable
	}
	body := map[string]any{}
	endpoint := "/embeddings"
	switch binding.Model.EmbeddingProtocol {
	case AgentEmbeddingProtocolVolcengineMultimodal:
		body = map[string]any{
			"model": binding.Model.ModelName,
			"input": []map[string]string{
				{"type": "text", "text": text},
			},
		}
		endpoint = "/embeddings/multimodal"
	default:
		body = map[string]any{
			"model":           binding.Model.ModelName,
			"input":           text,
			"encoding_format": "float",
		}
		if format := strings.TrimSpace(binding.Model.EncodingFormat); format != "" {
			body["encoding_format"] = format
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
		return nil, fmt.Errorf("%w: %s", ErrEmbeddingModelUnavailable, safelog.Text(err.Error(), 600))
	}
	defer resp.Body.Close()
	respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, openAIProbeMaxBodyBytes))
	if readErr != nil {
		return nil, readErr
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%w: %s", ErrEmbeddingModelUnavailable, safelog.Text(string(respBody), 600))
	}
	raw, ok := embeddingRawFromResponse(respBody)
	if !ok {
		return nil, fmt.Errorf("%w: embedding response missing vector", ErrEmbeddingModelUnavailable)
	}
	vector, ok := embeddingVectorFromRaw(raw)
	if !ok {
		return nil, fmt.Errorf("%w: embedding response vector is unreadable", ErrEmbeddingModelUnavailable)
	}
	if binding.Model.EmbeddingDimensions > 0 && binding.Model.EmbeddingDimensions != len(vector) {
		return nil, fmt.Errorf("%w: embedding dimension mismatch: got %d want %d", ErrEmbeddingModelUnavailable, len(vector), binding.Model.EmbeddingDimensions)
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
		out = append(out, float64(math.Float32frombits(binary.LittleEndian.Uint32(decoded[i:i+4]))))
	}
	return out, len(out) > 0
}

func normalizeEmbeddingObjectTypes(values []string) []string {
	if len(values) == 0 {
		return []string{EmbeddingObjectStockProfile, EmbeddingObjectNewsEvent}
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if (value == EmbeddingObjectStockProfile || value == EmbeddingObjectNewsEvent) && !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	if len(out) == 0 {
		return []string{EmbeddingObjectStockProfile, EmbeddingObjectNewsEvent}
	}
	return out
}

func normalizeEmbeddingRebuildLimit(limit int) int {
	if limit <= 0 {
		return 1000000
	}
	if limit > 1000000 {
		return 1000000
	}
	return limit
}

func trimEmbeddingInput(text string) string {
	text = strings.TrimSpace(text)
	runes := []rune(text)
	if len(runes) > 12000 {
		return string(runes[:12000])
	}
	return text
}

func stockProfileEmbeddingText(profile StockProfile) string {
	parts := []string{
		profile.Symbol,
		profile.Market,
		profile.InstrumentType,
		profile.Name,
		profile.Industry,
		strings.Join(profile.Sectors, " "),
		strings.Join(profile.Concepts, " "),
		strings.Join(profile.Tags, " "),
		profile.BusinessSummary,
		profile.BusinessSummaryZh,
		profile.BusinessSummaryEn,
		strings.Join(profile.KeywordsZh, " "),
		strings.Join(profile.KeywordsEn, " "),
		strings.Join(profile.BusinessLinesZh, " "),
		strings.Join(profile.BusinessLinesEn, " "),
		profile.Theme,
		profile.TrackingIndex,
		profile.ConstituentHint,
		profile.ProfileText,
		profile.ProfileTextZh,
		profile.ProfileTextEn,
	}
	return strings.Join(nonEmptyStrings(parts), "\n")
}

func newsEventEmbeddingText(event NewsEvent) string {
	parts := []string{
		event.Source,
		event.Title,
		event.Summary,
		event.Content,
		event.QualityStatus,
	}
	return strings.Join(nonEmptyStrings(parts), "\n")
}

func nonEmptyStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if text := strings.TrimSpace(value); text != "" {
			out = append(out, text)
		}
	}
	return out
}

func embeddingTextHash(text string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(text)))
	return hex.EncodeToString(sum[:])
}

func cosineSimilarity(a, b []float64) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

func embeddingErrorCode(err error) string {
	switch {
	case errors.Is(err, ErrEmbeddingModelNotConfigured):
		return ErrEmbeddingModelNotConfigured.Error()
	case errors.Is(err, ErrEmbeddingModelUnavailable):
		return ErrEmbeddingModelUnavailable.Error()
	case errors.Is(err, ErrEmbeddingAssetNotReady):
		return ErrEmbeddingAssetNotReady.Error()
	default:
		return ""
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
