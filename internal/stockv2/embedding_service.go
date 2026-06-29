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

const (
	defaultEmbeddingMaintainIntervalSeconds = 600
	defaultEmbeddingMaintainBatchSize       = 50
	defaultEmbeddingMaintainRateLimitMs     = 500
	maxEmbeddingMaintainBatchSize           = 200
	embeddingMaintenanceScanPageSize        = 200
)

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
	status := EmbeddingStatus{
		Config:       cfg,
		Status:       EmbeddingStatusModelNotConfigured,
		Code:         EmbeddingStatusModelNotConfigured,
		ErrorCode:    EmbeddingStatusModelNotConfigured,
		ErrorMessage: ErrEmbeddingModelNotConfigured.Error(),
		Message:      "embedding model is not configured",
		Maintenance: EmbeddingMaintenanceStatus{
			Enabled:    cfg.AutoMaintainEnabled,
			Running:    s.embeddingMaintenanceRunning(),
			LastRunAt:  cfg.LastMaintainAt,
			NextRunAt:  cfg.NextMaintainAt,
			LastResult: cfg.LastMaintainResult,
		},
	}
	if strings.TrimSpace(cfg.EmbeddingModelID) == "" {
		return status, nil
	}
	if !cfg.Enabled {
		status.Status = EmbeddingStatusModelUnavailable
		status.Code = EmbeddingStatusModelUnavailable
		status.ErrorCode = EmbeddingStatusModelUnavailable
		status.ErrorMessage = ErrEmbeddingModelUnavailable.Error()
		status.Message = "embedding model is disabled"
		return status, nil
	}
	model, err := s.store.GetAgentModelProfile(ctx, cfg.EmbeddingModelID)
	if err != nil {
		status.Status = EmbeddingStatusModelUnavailable
		status.Code = EmbeddingStatusModelUnavailable
		status.ErrorCode = EmbeddingStatusModelUnavailable
		status.ErrorMessage = ErrEmbeddingModelUnavailable.Error()
		status.Message = "embedding model not found"
		return status, nil
	}
	status.Model = &model
	status.ModelID = model.ID
	status.ProviderID = model.ProviderID
	status.ModelName = model.ModelName
	status.EmbeddingProtocol = model.EmbeddingProtocol
	status.EmbeddingDimensions = model.EmbeddingDimensions

	ready, _ := s.store.CountEmbeddingAssets(ctx, EmbeddingAssetListFilter{
		ModelID: model.ID,
		Status:  EmbeddingAssetStatusReady,
	})
	stale, _ := s.store.CountEmbeddingAssets(ctx, EmbeddingAssetListFilter{Status: EmbeddingAssetStatusStale})
	failed, _ := s.store.CountEmbeddingAssets(ctx, EmbeddingAssetListFilter{
		ModelID: model.ID,
		Status:  EmbeddingAssetStatusFailed,
	})
	status.ReadyAssets = ready
	status.StaleAssets = stale
	status.FailedAssets = failed
	status.ReadyAssetCount = ready
	status.StaleAssetCount = stale
	status.FailedAssetCount = failed
	if missing, err := s.countMissingEmbeddingSources(ctx, normalizeEmbeddingObjectTypes(nil), model); err == nil {
		status.MissingAssetCount = missing
	}
	status.AssetBreakdown = s.embeddingAssetBreakdown(ctx, model)

	if err := validateEmbeddingModel(model); err != nil {
		status.Status = EmbeddingStatusModelUnavailable
		status.Code = EmbeddingStatusModelUnavailable
		status.ErrorCode = EmbeddingStatusModelUnavailable
		status.ErrorMessage = ErrEmbeddingModelUnavailable.Error()
		status.Message = err.Error()
		return status, nil
	}
	if ready == 0 {
		status.Status = EmbeddingStatusAssetNotReady
		status.Code = EmbeddingStatusAssetNotReady
		status.ErrorCode = EmbeddingStatusAssetNotReady
		status.ErrorMessage = ErrEmbeddingAssetNotReady.Error()
		status.Message = "embedding assets are empty or stale; rebuild first"
		return status, nil
	}
	status.Ready = true
	status.Available = true
	status.Status = EmbeddingStatusReady
	status.Code = ""
	status.ErrorCode = ""
	status.ErrorMessage = ""
	status.Message = "embedding is ready"
	return status, nil
}

func (s *Service) UpdateEmbeddingConfig(ctx context.Context, req RequestUpdateEmbeddingConfig) (EmbeddingStatus, error) {
	cfg, err := s.embeddingConfigOrDefault(ctx)
	if err != nil {
		return EmbeddingStatus{}, err
	}
	oldModelID := strings.TrimSpace(cfg.EmbeddingModelID)
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
	if req.AutoMaintainEnabled != nil {
		cfg.AutoMaintainEnabled = *req.AutoMaintainEnabled
	}
	if req.MaintainIntervalSeconds != nil {
		cfg.MaintainIntervalSeconds = *req.MaintainIntervalSeconds
	}
	if req.MaintainBatchSize != nil {
		cfg.MaintainBatchSize = *req.MaintainBatchSize
	}
	if req.MaintainRateLimitMs != nil {
		cfg.MaintainRateLimitMs = *req.MaintainRateLimitMs
	}
	if strings.TrimSpace(cfg.EmbeddingModelID) == "" {
		cfg.Enabled = false
		cfg.AutoMaintainEnabled = false
	}
	modelChanged := req.EmbeddingModelID != nil && oldModelID != strings.TrimSpace(cfg.EmbeddingModelID)
	enabledRequested := req.Enabled != nil && *req.Enabled
	if cfg.Enabled && strings.TrimSpace(cfg.EmbeddingModelID) != "" && req.AutoMaintainEnabled == nil && (modelChanged || enabledRequested) {
		cfg.AutoMaintainEnabled = true
	}
	cfg = normalizeEmbeddingConfig(cfg)
	cfg.LastProbeStatus = EmbeddingStatusModelNotConfigured
	cfg.LastError = ""
	if cfg.Enabled {
		model, err := s.store.GetAgentModelProfile(ctx, cfg.EmbeddingModelID)
		if err != nil {
			return EmbeddingStatus{}, err
		}
		if err := validateEmbeddingModel(model); err != nil {
			return EmbeddingStatus{}, err
		}
		cfg.LastProbeAt = time.Now()
		cfg.LastProbeStatus = EmbeddingStatusReady
		if cfg.AutoMaintainEnabled && cfg.NextMaintainAt.IsZero() {
			cfg.NextMaintainAt = time.Now()
		}
	} else {
		cfg.AutoMaintainEnabled = false
		cfg.NextMaintainAt = time.Time{}
	}
	if _, err := s.store.UpsertEmbeddingConfig(ctx, cfg); err != nil {
		return EmbeddingStatus{}, err
	}
	if oldModelID != "" && oldModelID != strings.TrimSpace(cfg.EmbeddingModelID) {
		if err := s.store.MarkEmbeddingAssetsStaleForModelChange(ctx, cfg.EmbeddingModelID); err != nil {
			return EmbeddingStatus{}, err
		}
	}
	if cfg.Enabled && cfg.AutoMaintainEnabled {
		s.StartBackground(context.Background())
	}
	return s.GetEmbeddingStatus(ctx)
}

func (s *Service) RebuildEmbeddingAssets(ctx context.Context, req RequestRebuildEmbeddingAssets) (EmbeddingRebuildResult, error) {
	return s.RunEmbeddingMaintenanceBatch(ctx, req)
}

func (s *Service) RunEmbeddingMaintenanceBatch(ctx context.Context, req RequestRebuildEmbeddingAssets) (EmbeddingRebuildResult, error) {
	if !s.beginEmbeddingMaintenance() {
		return EmbeddingRebuildResult{
			Status:    "running",
			Message:   "embedding maintenance is already running",
			UpdatedAt: time.Now(),
		}, nil
	}
	defer s.endEmbeddingMaintenance()

	model, cfg, err := s.ensureEmbeddingModelReady(ctx)
	if err != nil {
		return EmbeddingRebuildResult{}, err
	}
	limit := req.Limit
	if limit <= 0 {
		limit = cfg.MaintainBatchSize
	}
	if limit <= 0 {
		limit = defaultEmbeddingMaintainBatchSize
	}
	if limit > maxEmbeddingMaintainBatchSize {
		limit = maxEmbeddingMaintainBatchSize
	}
	objectTypes := normalizeEmbeddingObjectTypes(req.ObjectTypes)
	sources, err := s.collectEmbeddingWorkSources(ctx, objectTypes, model, limit, req.Force)
	if err != nil {
		return EmbeddingRebuildResult{}, err
	}
	result := EmbeddingRebuildResult{
		Status:      "completed",
		ObjectTypes: objectTypes,
		Total:       len(sources),
		UpdatedAt:   time.Now(),
	}
	if len(sources) == 0 {
		s.recordEmbeddingMaintenanceResult(ctx, cfg, result)
		return result, nil
	}
	for idx, source := range sources {
		text := strings.TrimSpace(source.Text)
		if text == "" {
			result.Skipped++
			continue
		}
		textHash := hashEmbeddingText(text)
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
		result.Success++
		if cfg.MaintainRateLimitMs > 0 && idx < len(sources)-1 {
			select {
			case <-ctx.Done():
				result.Status = "partial"
				result.Message = safelog.Text(ctx.Err().Error(), 240)
				s.recordEmbeddingMaintenanceResult(ctx, cfg, result)
				return result, ctx.Err()
			case <-time.After(time.Duration(cfg.MaintainRateLimitMs) * time.Millisecond):
			}
		}
	}
	if result.Failed > 0 && result.Succeeded == 0 {
		result.Status = "failed"
	} else if result.Failed > 0 {
		result.Status = "partial"
	}
	s.recordEmbeddingMaintenanceResult(ctx, cfg, result)
	return result, nil
}

func (s *Service) recordEmbeddingMaintenanceResult(ctx context.Context, cfg EmbeddingConfig, result EmbeddingRebuildResult) {
	now := time.Now()
	cfg.LastProbeAt = time.Now()
	cfg.LastProbeStatus = result.Status
	cfg.LastError = ""
	if result.Failed > 0 {
		cfg.LastError = fmt.Sprintf("%d embedding assets failed", result.Failed)
	}
	cfg.LastMaintainAt = now
	cfg.NextMaintainAt = now.Add(time.Duration(cfg.MaintainIntervalSeconds) * time.Second)
	cfg.LastMaintainResult = embeddingMaintenanceResultText(result)
	_, _ = s.store.UpsertEmbeddingConfig(ctx, cfg)
}

func (s *Service) ListEmbeddingAssets(ctx context.Context, filter EmbeddingAssetListFilter) ([]EmbeddingAsset, error) {
	filter.Limit = normalizedPageLimit(filter.Limit, 200)
	filter.Offset = normalizedPageOffset(filter.Offset)
	return s.store.ListEmbeddingAssets(ctx, filter)
}

func (s *Service) CountEmbeddingAssets(ctx context.Context, filter EmbeddingAssetListFilter) (int, error) {
	return s.store.CountEmbeddingAssets(ctx, filter)
}

func (s *Service) embeddingAssetBreakdown(ctx context.Context, model AgentModelProfile) []EmbeddingAssetBreakdown {
	categories := []struct {
		category    string
		objectTypes []string
	}{
		{category: EmbeddingObjectStockProfile, objectTypes: []string{EmbeddingObjectStockProfile}},
		{category: EmbeddingObjectNewsEvent, objectTypes: []string{EmbeddingObjectNewsEvent}},
		{category: "other", objectTypes: []string{EmbeddingObjectOpportunity}},
	}
	out := make([]EmbeddingAssetBreakdown, 0, len(categories))
	for _, category := range categories {
		item := EmbeddingAssetBreakdown{Category: category.category}
		for _, objectType := range category.objectTypes {
			if ready, err := s.store.CountEmbeddingAssets(ctx, EmbeddingAssetListFilter{
				ObjectType: objectType,
				ModelID:    model.ID,
				Status:     EmbeddingAssetStatusReady,
			}); err == nil {
				item.ReadyAssetCount += ready
			}
			if missing, err := s.countMissingEmbeddingSources(ctx, []string{objectType}, model); err == nil {
				item.MissingAssetCount += missing
			}
		}
		out = append(out, item)
	}
	return out
}

func (s *Service) runEmbeddingMaintenanceScheduler(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	s.maybeRunEmbeddingMaintenance(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.maybeRunEmbeddingMaintenance(ctx)
		}
	}
}

func (s *Service) maybeRunEmbeddingMaintenance(ctx context.Context) {
	cfg, err := s.embeddingConfigOrDefault(ctx)
	if err != nil {
		if s.log != nil {
			s.log.Warn("stockv2 embedding maintenance config unavailable", "error", safelog.Text(err.Error(), 300))
		}
		return
	}
	if !cfg.Enabled || !cfg.AutoMaintainEnabled || strings.TrimSpace(cfg.EmbeddingModelID) == "" {
		return
	}
	now := time.Now()
	if !cfg.NextMaintainAt.IsZero() && cfg.NextMaintainAt.After(now) {
		return
	}
	if _, err := s.RunEmbeddingMaintenanceBatch(ctx, RequestRebuildEmbeddingAssets{
		ObjectTypes: normalizeEmbeddingObjectTypes(nil),
		Limit:       cfg.MaintainBatchSize,
	}); err != nil {
		cfg.LastMaintainAt = now
		cfg.NextMaintainAt = now.Add(time.Duration(cfg.MaintainIntervalSeconds) * time.Second)
		cfg.LastProbeStatus = "failed"
		cfg.LastError = safelog.Text(err.Error(), 500)
		cfg.LastMaintainResult = "failed: " + safelog.Text(err.Error(), 240)
		_, _ = s.store.UpsertEmbeddingConfig(context.Background(), cfg)
		if s.log != nil {
			s.log.Warn("stockv2 embedding maintenance failed", "error", safelog.Text(err.Error(), 300))
		}
	}
}

func (s *Service) hasEmbeddingAutoMaintenanceEnabled(ctx context.Context) bool {
	cfg, err := s.embeddingConfigOrDefault(ctx)
	if err != nil {
		return false
	}
	return cfg.Enabled && cfg.AutoMaintainEnabled && strings.TrimSpace(cfg.EmbeddingModelID) != ""
}

func (s *Service) SemanticSearch(ctx context.Context, objectType, query string, limit int) ([]SemanticSearchHit, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, ErrInvalidEmbeddingRequest
	}
	model, _, err := s.ensureEmbeddingModelReady(ctx)
	if err != nil {
		return nil, err
	}
	ready, err := s.store.CountEmbeddingAssets(ctx, EmbeddingAssetListFilter{
		ObjectType: objectType,
		ModelID:    model.ID,
		Status:     EmbeddingAssetStatusReady,
		Dimensions: model.EmbeddingDimensions,
	})
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
		if err != nil ||
			asset.Status != EmbeddingAssetStatusReady ||
			asset.ModelID != model.ID ||
			asset.EmbeddingDimensions != len(vector) {
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
	if len(out) == 0 {
		return nil, ErrEmbeddingAssetNotReady
	}
	return out, nil
}

func (s *Service) SemanticSearchStockProfiles(ctx context.Context, req SemanticSearchRequest) ([]SemanticStockProfileResult, error) {
	hits, err := s.SemanticSearch(ctx, EmbeddingObjectStockProfile, req.Query, req.Limit)
	if err != nil {
		return nil, err
	}
	out := make([]SemanticStockProfileResult, 0, len(hits))
	for _, hit := range hits {
		if req.MinScore != 0 && hit.Score < req.MinScore {
			continue
		}
		if hit.Profile == nil {
			continue
		}
		out = append(out, SemanticStockProfileResult{Score: hit.Score, Profile: *hit.Profile, Asset: hit.Asset})
	}
	return out, nil
}

func (s *Service) SemanticSearchNewsEvents(ctx context.Context, req SemanticSearchRequest) ([]SemanticNewsEventResult, error) {
	hits, err := s.SemanticSearch(ctx, EmbeddingObjectNewsEvent, req.Query, req.Limit)
	if err != nil {
		return nil, err
	}
	out := make([]SemanticNewsEventResult, 0, len(hits))
	for _, hit := range hits {
		if req.MinScore != 0 && hit.Score < req.MinScore {
			continue
		}
		if hit.NewsEvent == nil {
			continue
		}
		out = append(out, SemanticNewsEventResult{Score: hit.Score, Event: *hit.NewsEvent, Asset: hit.Asset})
	}
	return out, nil
}

func (s *Service) embeddingConfigOrDefault(ctx context.Context) (EmbeddingConfig, error) {
	cfg, err := s.store.GetEmbeddingConfig(ctx)
	if err == nil {
		return normalizeEmbeddingConfig(cfg), nil
	}
	if !errors.Is(err, ErrEmbeddingConfigNotFound) {
		return EmbeddingConfig{}, err
	}
	return s.store.UpsertEmbeddingConfig(ctx, EmbeddingConfig{
		ID:                      EmbeddingConfigIDDefault,
		Enabled:                 false,
		MaintainIntervalSeconds: defaultEmbeddingMaintainIntervalSeconds,
		MaintainBatchSize:       defaultEmbeddingMaintainBatchSize,
		MaintainRateLimitMs:     defaultEmbeddingMaintainRateLimitMs,
		LastProbeStatus:         EmbeddingStatusModelNotConfigured,
	})
}

func normalizeEmbeddingConfig(cfg EmbeddingConfig) EmbeddingConfig {
	if cfg.ID == "" {
		cfg.ID = EmbeddingConfigIDDefault
	}
	if cfg.MaintainIntervalSeconds <= 0 {
		cfg.MaintainIntervalSeconds = defaultEmbeddingMaintainIntervalSeconds
	}
	if cfg.MaintainBatchSize <= 0 {
		cfg.MaintainBatchSize = defaultEmbeddingMaintainBatchSize
	}
	if cfg.MaintainBatchSize > maxEmbeddingMaintainBatchSize {
		cfg.MaintainBatchSize = maxEmbeddingMaintainBatchSize
	}
	if cfg.MaintainRateLimitMs < 0 {
		cfg.MaintainRateLimitMs = defaultEmbeddingMaintainRateLimitMs
	}
	return cfg
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
		return AgentModelProfile{}, cfg, ErrEmbeddingModelUnavailable
	}
	model, err := s.store.GetAgentModelProfile(ctx, cfg.EmbeddingModelID)
	if err != nil {
		return AgentModelProfile{}, cfg, ErrEmbeddingModelUnavailable
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
	// 维度可选：火山引擎 / OpenAI 等 embedding 的维度由 API 返回决定，用户无需预设。
	// rebuild 时以实际向量长度为准（asset.EmbeddingDimensions = len(vector)），向量库
	// stockv2_embedding_vectors 用长表 (vector_ref, dim_index, value) 存储，不依赖固定维度。
	// 若用户填了维度，rebuild / 搜索阶段会据此做一致性校验（见 validateEmbeddingDimensions）。
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
	if len(out) == 0 {
		return []string{EmbeddingObjectStockProfile, EmbeddingObjectNewsEvent, EmbeddingObjectOpportunity}
	}
	return out
}

func (s *Service) beginEmbeddingMaintenance() bool {
	s.embeddingMu.Lock()
	defer s.embeddingMu.Unlock()
	if s.embeddingRun {
		return false
	}
	s.embeddingRun = true
	return true
}

func (s *Service) endEmbeddingMaintenance() {
	s.embeddingMu.Lock()
	s.embeddingRun = false
	s.embeddingMu.Unlock()
}

func (s *Service) embeddingMaintenanceRunning() bool {
	s.embeddingMu.Lock()
	defer s.embeddingMu.Unlock()
	return s.embeddingRun
}

func embeddingMaintenanceResultText(result EmbeddingRebuildResult) string {
	return fmt.Sprintf("status=%s total=%d success=%d skipped=%d failed=%d", result.Status, result.Total, result.Success, result.Skipped, result.Failed)
}

func (s *Service) collectEmbeddingWorkSources(ctx context.Context, objectTypes []string, model AgentModelProfile, limit int, force bool) ([]embeddingAssetSource, error) {
	if limit <= 0 {
		return nil, nil
	}
	var forced []embeddingAssetSource
	var missing []embeddingAssetSource
	var stale []embeddingAssetSource
	var failed []embeddingAssetSource
	var changed []embeddingAssetSource
	appendBounded := func(items *[]embeddingAssetSource, source embeddingAssetSource) {
		if len(*items) < limit {
			*items = append(*items, source)
		}
	}
	// ponytail: this scans current business rows instead of adding a queue table; upgrade to a
	// persistent queue only if asset volume or SLA needs strict incremental checkpoints.
	err := s.forEachEmbeddingSource(ctx, objectTypes, func(source embeddingAssetSource) error {
		text := strings.TrimSpace(source.Text)
		if text == "" {
			return nil
		}
		if force {
			appendBounded(&forced, source)
			return nil
		}
		existing, err := s.store.GetEmbeddingAssetByObject(ctx, source.ObjectType, source.ObjectID, model.ID)
		if err != nil {
			if errors.Is(err, ErrEmbeddingAssetNotFound) {
				appendBounded(&missing, source)
				return nil
			}
			return err
		}
		switch existing.Status {
		case EmbeddingAssetStatusStale:
			appendBounded(&stale, source)
		case EmbeddingAssetStatusFailed:
			appendBounded(&failed, source)
		case EmbeddingAssetStatusReady:
			textHash := hashEmbeddingText(text)
			if existing.TextHash != textHash || (model.EmbeddingDimensions > 0 && existing.EmbeddingDimensions != model.EmbeddingDimensions) {
				appendBounded(&changed, source)
			}
		default:
			appendBounded(&failed, source)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if force {
		return forced, nil
	}
	out := make([]embeddingAssetSource, 0, limit)
	for _, group := range [][]embeddingAssetSource{missing, stale, failed, changed} {
		for _, source := range group {
			if len(out) >= limit {
				return out, nil
			}
			out = append(out, source)
		}
	}
	return out, nil
}

func (s *Service) countMissingEmbeddingSources(ctx context.Context, objectTypes []string, model AgentModelProfile) (int, error) {
	total := 0
	err := s.forEachEmbeddingSource(ctx, objectTypes, func(source embeddingAssetSource) error {
		if strings.TrimSpace(source.Text) == "" {
			return nil
		}
		if _, err := s.store.GetEmbeddingAssetByObject(ctx, source.ObjectType, source.ObjectID, model.ID); err != nil {
			if errors.Is(err, ErrEmbeddingAssetNotFound) {
				total++
				return nil
			}
			return err
		}
		return nil
	})
	return total, err
}

func (s *Service) forEachEmbeddingSource(ctx context.Context, objectTypes []string, visit func(embeddingAssetSource) error) error {
	for _, objectType := range objectTypes {
		offset := 0
		for {
			items, err := s.listEmbeddingSourcesPage(ctx, objectType, embeddingMaintenanceScanPageSize, offset)
			if err != nil {
				return err
			}
			if len(items) == 0 {
				break
			}
			for _, item := range items {
				if err := visit(item); err != nil {
					return err
				}
			}
			if len(items) < embeddingMaintenanceScanPageSize {
				break
			}
			offset += len(items)
		}
	}
	return nil
}

func (s *Service) listEmbeddingSourcesPage(ctx context.Context, objectType string, limit, offset int) ([]embeddingAssetSource, error) {
	switch objectType {
	case EmbeddingObjectStockProfile:
		items, err := s.store.ListStockProfiles(ctx, StockProfileListFilter{Limit: limit, Offset: offset})
		if err != nil {
			return nil, err
		}
		out := make([]embeddingAssetSource, 0, len(items))
		for _, item := range items {
			out = append(out, embeddingAssetSource{ObjectType: objectType, ObjectID: item.Symbol, Text: stockProfileEmbeddingText(item)})
		}
		return out, nil
	case EmbeddingObjectNewsEvent:
		items, err := s.store.ListNewsEvents(ctx, NewsEventListFilter{Limit: limit, Offset: offset})
		if err != nil {
			return nil, err
		}
		out := make([]embeddingAssetSource, 0, len(items))
		for _, item := range items {
			out = append(out, embeddingAssetSource{ObjectType: objectType, ObjectID: item.ID, Text: newsEventEmbeddingText(item)})
		}
		return out, nil
	case EmbeddingObjectOpportunity:
		items, err := s.store.ListOpportunities(ctx, OpportunityListFilter{Limit: limit, Offset: offset})
		if err != nil {
			return nil, err
		}
		out := make([]embeddingAssetSource, 0, len(items))
		for _, item := range items {
			out = append(out, embeddingAssetSource{ObjectType: objectType, ObjectID: item.ID, Text: item.Title + "\n" + item.UserThesis})
		}
		return out, nil
	default:
		return nil, nil
	}
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
	parts := []string{
		item.Symbol,
		item.Market,
		item.InstrumentType,
		item.Name,
		item.Industry,
		strings.Join(item.Sectors, " "),
		strings.Join(item.Concepts, " "),
		strings.Join(item.Tags, " "),
		item.BusinessSummary,
		item.BusinessSummaryZh,
		item.BusinessSummaryEn,
		strings.Join(item.KeywordsZh, " "),
		strings.Join(item.KeywordsEn, " "),
		strings.Join(item.BusinessLinesZh, " "),
		strings.Join(item.BusinessLinesEn, " "),
		item.Theme,
		item.TrackingIndex,
		item.ConstituentHint,
		item.ProfileText,
		item.ProfileTextZh,
		item.ProfileTextEn,
	}
	return strings.Join(nonEmptyStrings(parts), "\n")
}

func newsEventEmbeddingText(item NewsEvent) string {
	return strings.Join(nonEmptyStrings([]string{item.Source, item.Title, item.Summary, item.Content, item.QualityStatus}), "\n")
}

func hashEmbeddingText(text string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(text)))
	return hex.EncodeToString(sum[:])
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

func (s *Service) generateEmbedding(ctx context.Context, model AgentModelProfile, input string) ([]float64, error) {
	provider, err := s.store.GetAgentProviderProfile(ctx, model.ProviderID)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrEmbeddingModelUnavailable, safelog.Text(err.Error(), 300))
	}
	if isDefaultCodexCLIProvider(provider) {
		return nil, ErrEmbeddingModelUnavailable
	}
	baseURL, apiKey, err := agentProviderOpenAIConfig(provider)
	if err != nil {
		return nil, ErrEmbeddingModelUnavailable
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
		return nil, fmt.Errorf("%w: %s", ErrEmbeddingModelUnavailable, safelog.Text(err.Error(), 600))
	}
	defer resp.Body.Close()
	respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, openAIProbeMaxBodyBytes))
	if readErr != nil {
		return nil, fmt.Errorf("%w: %s", ErrEmbeddingModelUnavailable, safelog.Text(readErr.Error(), 600))
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

func embeddingErrorCode(err error) string {
	switch {
	case errors.Is(err, ErrEmbeddingModelNotConfigured):
		return EmbeddingStatusModelNotConfigured
	case errors.Is(err, ErrEmbeddingModelUnavailable), errors.Is(err, ErrEmbeddingModelInvalid), errors.Is(err, ErrEmbeddingDimensionsMismatch):
		return EmbeddingStatusModelUnavailable
	case errors.Is(err, ErrEmbeddingAssetNotReady):
		return EmbeddingStatusAssetNotReady
	default:
		return ""
	}
}
