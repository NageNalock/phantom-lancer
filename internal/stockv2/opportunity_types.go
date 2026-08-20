package stockv2

import (
	"encoding/json"
	"errors"
	"strings"
	"time"
)

const (
	OpportunityStatusDraft       = "draft"
	OpportunityStatusResearching = "researching"
	OpportunityStatusCompleted   = "completed"
	OpportunityStatusClosed      = "closed"

	OpportunityMarketScopeAShare = "a_share"
	OpportunityMarketScopeHK     = "hk"
	OpportunityMarketScopeUS     = "us"
	OpportunityMarketScopeAll    = "all"

	OpportunityInstrumentScopeStock        = "stock"
	OpportunityInstrumentScopeExchangeFund = "exchange_fund"
	OpportunityInstrumentScopeBoth         = "both"

	OpportunityDiscoveryRunStatusPending   = "pending"
	OpportunityDiscoveryRunStatusRunning   = "running"
	OpportunityDiscoveryRunStatusCompleted = "completed"
	OpportunityDiscoveryRunStatusFailed    = "failed"
	OpportunityDiscoveryRunStatusCancelled = "cancelled"

	OpportunityDiscoveryStepStatusPending   = "pending"
	OpportunityDiscoveryStepStatusRunning   = "running"
	OpportunityDiscoveryStepStatusCompleted = "completed"
	OpportunityDiscoveryStepStatusFailed    = "failed"

	OpportunityEvidenceSourceInternalProfile = "internal_profile"
	OpportunityEvidenceSourceInternalNews    = "internal_news"
	OpportunityEvidenceSourceQuote           = "quote"
	OpportunityEvidenceSourceDailyBar        = "daily_bar"
	OpportunityEvidenceSourceExternal        = "external_source"
	OpportunityEvidenceSourceAgentNote       = "agent_note"

	OpportunityCandidateStatusCandidate         = "candidate"
	OpportunityCandidateStatusShortlisted       = "shortlisted"
	OpportunityCandidateStatusRejected          = "rejected"
	OpportunityCandidateStatusStrategyRequested = "strategy_requested"
	OpportunityCandidateStatusStrategyGenerated = "strategy_generated"

	OpportunityRelationDirect      = "direct"
	OpportunityRelationSupplyChain = "supply_chain"
	OpportunityRelationThemeETF    = "theme_etf"
	OpportunityRelationCompetitor  = "competitor"
	OpportunityRelationWeak        = "weak"

	OpportunityDiscoveryReportSchemaVersion = "opportunity-discovery-report/v2"
	OpportunityDiscoveryOutputType          = "opportunity_discovery"
	OpportunityDiscoveryConfigIDDefault     = "default"

	EmbeddingConfigIDDefault = "stockv2-embedding-config"

	EmbeddingAssetStatusReady  = "ready"
	EmbeddingAssetStatusStale  = "stale"
	EmbeddingAssetStatusFailed = "failed"

	EmbeddingObjectStockProfile      = "stock_profile"
	EmbeddingObjectNewsEvent         = "news_event"
	EmbeddingObjectNewsThread        = "news_thread"
	EmbeddingObjectNewsThreadVersion = "news_thread_version"
	EmbeddingObjectRawNews           = "raw_news"
	EmbeddingObjectOpportunity       = "opportunity"
	EmbeddingObjectExternalSource    = "external_source"

	EmbeddingStatusReady              = "ready"
	EmbeddingStatusDisabled           = "disabled"
	EmbeddingStatusModelNotConfigured = "embedding_model_not_configured"
	EmbeddingStatusModelUnavailable   = "embedding_model_unavailable"
	EmbeddingStatusAssetNotReady      = "embedding_asset_not_ready"
)

var (
	ErrOpportunityNotFound          = errors.New("opportunity not found")
	ErrDiscoveryRunNotFound         = errors.New("opportunity discovery run not found")
	ErrDiscoveryStepNotFound        = errors.New("opportunity discovery step not found")
	ErrEvidenceNotFound             = errors.New("opportunity evidence not found")
	ErrOpportunityCandidateNotFound = errors.New("opportunity candidate not found")
	ErrOpportunityResultNotFound    = errors.New("opportunity result not found")

	ErrInvalidOpportunityInput        = errors.New("invalid opportunity input")
	ErrInvalidOpportunityStatus       = errors.New("invalid opportunity status")
	ErrInvalidDiscoveryRunStatus      = errors.New("invalid opportunity discovery run status")
	ErrInvalidDiscoveryStepStatus     = errors.New("invalid opportunity discovery step status")
	ErrInvalidOpportunityCandidate    = errors.New("invalid opportunity candidate")
	ErrInvalidOpportunityResult       = errors.New("invalid opportunity discovery result")
	ErrOpportunityUnsafeResult        = errors.New("opportunity discovery result contains forbidden trading or portfolio action")
	ErrOpportunitySymbolNotFound      = errors.New("opportunity candidate symbol not found in StockV2 master data")
	ErrOpportunityTaskMismatch        = errors.New("opportunity discovery task does not match run")
	ErrOpportunityCandidateOutOfScope = errors.New("opportunity candidate is outside the configured discovery scope")

	ErrEmbeddingConfigNotFound        = errors.New("embedding config not found")
	ErrEmbeddingAssetNotFound         = errors.New("embedding asset not found")
	ErrEmbeddingDisabled              = errors.New("embedding_model_unavailable")
	ErrEmbeddingModelNotConfigured    = errors.New("embedding_model_not_configured")
	ErrEmbeddingModelUnavailable      = errors.New("embedding_model_unavailable")
	ErrEmbeddingModelInvalid          = errors.New("embedding model must be enabled, available, and modelType=embedding")
	ErrEmbeddingDimensionsMismatch    = errors.New("embedding dimensions mismatch")
	ErrEmbeddingAssetNotReady         = errors.New("embedding_asset_not_ready")
	ErrInvalidEmbeddingConfig         = errors.New("invalid embedding config")
	ErrInvalidEmbeddingRequest        = errors.New("invalid embedding request")
	ErrEmbeddingRebuildNotImplemented = errors.New("embedding rebuild executor is not implemented")
)

type Opportunity struct {
	ID              string    `json:"id"`
	Title           string    `json:"title"`
	UserThesis      string    `json:"userThesis"`
	MarketScope     string    `json:"marketScope"`
	InstrumentScope string    `json:"instrumentScope"`
	Status          string    `json:"status"`
	CreatedBy       string    `json:"createdBy,omitempty"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

type OpportunityDiscoveryRun struct {
	ID                          string    `json:"id"`
	OpportunityID               string    `json:"opportunityId"`
	AgentRunID                  string    `json:"agentRunId,omitempty"`
	Status                      string    `json:"status"`
	CurrentStepID               string    `json:"currentStepId,omitempty"`
	StepTotal                   int       `json:"stepTotal"`
	StepCompleted               int       `json:"stepCompleted"`
	CandidateCount              int       `json:"candidateCount"`
	EvidenceCount               int       `json:"evidenceCount"`
	ExternalSourceCount         int       `json:"externalSourceCount"`
	ExcludeChiNextAndStarMarket bool      `json:"excludeChiNextAndStarMarket"`
	StartedAt                   time.Time `json:"startedAt,omitempty"`
	FinishedAt                  time.Time `json:"finishedAt,omitempty"`
	ErrorMessage                string    `json:"errorMessage,omitempty"`
	CreatedAt                   time.Time `json:"createdAt"`
	UpdatedAt                   time.Time `json:"updatedAt"`
}

type OpportunityDiscoveryConfig struct {
	ID                          string    `json:"id"`
	ExcludeChiNextAndStarMarket bool      `json:"excludeChiNextAndStarMarket"`
	UpdatedAt                   time.Time `json:"updatedAt"`
}

type OpportunityDiscoveryStep struct {
	ID            string         `json:"id"`
	RunID         string         `json:"runId"`
	StepKey       string         `json:"stepKey"`
	StepTitle     string         `json:"stepTitle"`
	Status        string         `json:"status"`
	OrderIndex    int            `json:"orderIndex"`
	InputSummary  string         `json:"inputSummary,omitempty"`
	OutputSummary string         `json:"outputSummary,omitempty"`
	Metadata      map[string]any `json:"metadata,omitempty"`
	StartedAt     time.Time      `json:"startedAt,omitempty"`
	FinishedAt    time.Time      `json:"finishedAt,omitempty"`
	CreatedAt     time.Time      `json:"createdAt"`
	UpdatedAt     time.Time      `json:"updatedAt"`
}

type OpportunityEvidence struct {
	ID          string         `json:"id"`
	RunID       string         `json:"runId"`
	CandidateID string         `json:"candidateId,omitempty"`
	SourceType  string         `json:"sourceType"`
	SourceRef   string         `json:"sourceRef,omitempty"`
	Title       string         `json:"title"`
	Summary     string         `json:"summary,omitempty"`
	URL         string         `json:"url,omitempty"`
	Publisher   string         `json:"publisher,omitempty"`
	PublishedAt time.Time      `json:"publishedAt,omitempty"`
	Confidence  float64        `json:"confidence,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
	CreatedAt   time.Time      `json:"createdAt"`
}

type OpportunityCandidate struct {
	ID              string                `json:"id"`
	OpportunityID   string                `json:"opportunityId"`
	RunID           string                `json:"runId"`
	Symbol          string                `json:"symbol"`
	Market          string                `json:"market"`
	InstrumentType  string                `json:"instrumentType"`
	Name            string                `json:"name"`
	RelationType    string                `json:"relationType"`
	RelevanceScore  float64               `json:"relevanceScore"`
	EvidenceScore   float64               `json:"evidenceScore"`
	MarketRiskScore float64               `json:"marketRiskScore"`
	Confidence      float64               `json:"confidence"`
	Rank            int                   `json:"rank"`
	Status          string                `json:"status"`
	Reason          string                `json:"reason,omitempty"`
	RiskSummary     string                `json:"riskSummary,omitempty"`
	HorizonOutlooks []ModelHorizonOutlook `json:"horizonOutlooks,omitempty"`
	Metadata        map[string]any        `json:"metadata,omitempty"`
	CreatedAt       time.Time             `json:"createdAt"`
	UpdatedAt       time.Time             `json:"updatedAt"`
}

type OpportunityResult struct {
	ID                    string         `json:"id"`
	RunID                 string         `json:"runId"`
	Summary               string         `json:"summary,omitempty"`
	Conclusion            string         `json:"conclusion,omitempty"`
	RecommendedNextAction string         `json:"recommendedNextAction,omitempty"`
	RawResult             map[string]any `json:"rawResult,omitempty"`
	CreatedAt             time.Time      `json:"createdAt"`
}

type EmbeddingConfig struct {
	ID                      string    `json:"id"`
	EmbeddingModelID        string    `json:"embeddingModelId,omitempty"`
	Enabled                 bool      `json:"enabled"`
	AutoMaintainEnabled     bool      `json:"autoMaintainEnabled"`
	MaintainIntervalSeconds int       `json:"maintainIntervalSeconds"`
	MaintainBatchSize       int       `json:"maintainBatchSize"`
	MaintainRateLimitMs     int       `json:"maintainRateLimitMs"`
	LastProbeAt             time.Time `json:"lastProbeAt,omitempty"`
	LastProbeStatus         string    `json:"lastProbeStatus,omitempty"`
	LastError               string    `json:"lastError,omitempty"`
	LastMaintainAt          time.Time `json:"lastMaintainAt,omitempty"`
	NextMaintainAt          time.Time `json:"nextMaintainAt,omitempty"`
	LastMaintainResult      string    `json:"lastMaintainResult,omitempty"`
	UpdatedAt               time.Time `json:"updatedAt"`
}

type EmbeddingAsset struct {
	ID                  string    `json:"id"`
	ObjectType          string    `json:"objectType"`
	ObjectID            string    `json:"objectId"`
	TextHash            string    `json:"textHash"`
	TextSummary         string    `json:"textSummary,omitempty"`
	ModelID             string    `json:"modelId"`
	ProviderID          string    `json:"providerId,omitempty"`
	EmbeddingProtocol   string    `json:"embeddingProtocol,omitempty"`
	EmbeddingDimensions int       `json:"embeddingDimensions"`
	VectorRef           string    `json:"vectorRef,omitempty"`
	Status              string    `json:"status"`
	ErrorMessage        string    `json:"errorMessage,omitempty"`
	CreatedAt           time.Time `json:"createdAt"`
	UpdatedAt           time.Time `json:"updatedAt"`
}

type RequestCreateOpportunity struct {
	Title           string `json:"title"`
	UserThesis      string `json:"userThesis"`
	MarketScope     string `json:"marketScope,omitempty"`
	InstrumentScope string `json:"instrumentScope,omitempty"`
	CreatedBy       string `json:"createdBy,omitempty"`
}

type RequestUpdateOpportunity struct {
	Title           *string `json:"title,omitempty"`
	UserThesis      *string `json:"userThesis,omitempty"`
	MarketScope     *string `json:"marketScope,omitempty"`
	InstrumentScope *string `json:"instrumentScope,omitempty"`
	Status          *string `json:"status,omitempty"`
}

type RequestStartOpportunityDiscoveryRun struct {
	RequestedBy string `json:"requestedBy,omitempty"`
	// MarketScanRunID is internal orchestration context and is never accepted from HTTP.
	MarketScanRunID string `json:"-"`
}

type RequestUpdateOpportunityDiscoveryConfig struct {
	ExcludeChiNextAndStarMarket *bool `json:"excludeChiNextAndStarMarket,omitempty"`
}

type RequestUpdateOpportunityCandidate struct {
	Status      *string        `json:"status,omitempty"`
	Reason      *string        `json:"reason,omitempty"`
	RiskSummary *string        `json:"riskSummary,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

type RequestGenerateStrategyFromOpportunityCandidate struct {
	RequestedBy string `json:"requestedBy,omitempty"`
	UserGoal    string `json:"userGoal,omitempty"`
	TimeHorizon string `json:"timeHorizon,omitempty"`
}

type RequestUpdateEmbeddingConfig struct {
	EmbeddingModelID        *string `json:"embeddingModelId,omitempty"`
	Enabled                 *bool   `json:"enabled,omitempty"`
	AutoMaintainEnabled     *bool   `json:"autoMaintainEnabled,omitempty"`
	MaintainIntervalSeconds *int    `json:"maintainIntervalSeconds,omitempty"`
	MaintainBatchSize       *int    `json:"maintainBatchSize,omitempty"`
	MaintainRateLimitMs     *int    `json:"maintainRateLimitMs,omitempty"`
}

type RequestRebuildEmbeddingAssets struct {
	ObjectTypes []string `json:"objectTypes,omitempty"`
	Force       bool     `json:"force,omitempty"`
	Limit       int      `json:"limit,omitempty"`
}

type EmbeddingStatus struct {
	Ready               bool                       `json:"ready"`
	Available           bool                       `json:"available"`
	Status              string                     `json:"status"`
	Code                string                     `json:"code,omitempty"`
	Message             string                     `json:"message,omitempty"`
	ErrorCode           string                     `json:"errorCode,omitempty"`
	ErrorMessage        string                     `json:"errorMessage,omitempty"`
	Config              EmbeddingConfig            `json:"config"`
	Model               *AgentModelProfile         `json:"model,omitempty"`
	ModelID             string                     `json:"modelId,omitempty"`
	ProviderID          string                     `json:"providerId,omitempty"`
	ModelName           string                     `json:"modelName,omitempty"`
	EmbeddingProtocol   string                     `json:"embeddingProtocol,omitempty"`
	EmbeddingDimensions int                        `json:"embeddingDimensions,omitempty"`
	ReadyAssets         int                        `json:"readyAssets"`
	StaleAssets         int                        `json:"staleAssets"`
	FailedAssets        int                        `json:"failedAssets"`
	MissingAssetCount   int                        `json:"missingAssetCount"`
	ReadyAssetCount     int                        `json:"readyAssetCount"`
	StaleAssetCount     int                        `json:"staleAssetCount"`
	FailedAssetCount    int                        `json:"failedAssetCount"`
	AssetBreakdown      []EmbeddingAssetBreakdown  `json:"assetBreakdown,omitempty"`
	Maintenance         EmbeddingMaintenanceStatus `json:"maintenance"`
}

type EmbeddingAssetBreakdown struct {
	Category          string `json:"category"`
	ReadyAssetCount   int    `json:"readyAssetCount"`
	MissingAssetCount int    `json:"missingAssetCount"`
}

type EmbeddingMaintenanceStatus struct {
	Enabled    bool      `json:"enabled"`
	Running    bool      `json:"running"`
	LastRunAt  time.Time `json:"lastRunAt,omitempty"`
	NextRunAt  time.Time `json:"nextRunAt,omitempty"`
	LastResult string    `json:"lastResult,omitempty"`
}

type EmbeddingRebuildResult struct {
	Status      string          `json:"status"`
	Message     string          `json:"message,omitempty"`
	ObjectTypes []string        `json:"objectTypes,omitempty"`
	Total       int             `json:"total"`
	Succeeded   int             `json:"succeeded"`
	Success     int             `json:"success"`
	Skipped     int             `json:"skipped"`
	Failed      int             `json:"failed"`
	FailedItems []UpdateFailure `json:"failedItems,omitempty"`
	UpdatedAt   time.Time       `json:"updatedAt,omitempty"`
}

type OpportunityDiscoveryReport struct {
	SchemaVersion         string                                `json:"schema_version"`
	OpportunityID         string                                `json:"opportunity_id"`
	Summary               string                                `json:"summary"`
	ThemeChain            []OpportunityDiscoveryThemeChainItem  `json:"theme_chain,omitempty"`
	Candidates            []OpportunityDiscoveryReportCandidate `json:"candidates"`
	Excluded              []OpportunityDiscoveryReportExclusion `json:"excluded,omitempty"`
	DataQualityNotes      []string                              `json:"data_quality_notes,omitempty"`
	ExternalSources       []map[string]any                      `json:"external_sources,omitempty"`
	Conclusion            string                                `json:"conclusion,omitempty"`
	RecommendedNextAction string                                `json:"recommended_next_action,omitempty"`
}

type OpportunityDiscoveryThemeChainItem struct {
	Layer           string   `json:"layer"`
	Rank            int      `json:"rank,omitempty"`
	Representatives []string `json:"representatives,omitempty"`
	Scarcity        string   `json:"scarcity,omitempty"`
}

func (i *OpportunityDiscoveryThemeChainItem) UnmarshalJSON(data []byte) error {
	var layer string
	if err := json.Unmarshal(data, &layer); err == nil {
		if strings.TrimSpace(layer) == "" {
			return errors.New("theme chain layer is required")
		}
		*i = OpportunityDiscoveryThemeChainItem{Layer: strings.TrimSpace(layer)}
		return nil
	}
	type themeChainItem OpportunityDiscoveryThemeChainItem
	var item themeChainItem
	if err := json.Unmarshal(data, &item); err != nil {
		return err
	}
	item.Layer = strings.TrimSpace(item.Layer)
	if item.Layer == "" {
		return errors.New("theme chain layer is required")
	}
	*i = OpportunityDiscoveryThemeChainItem(item)
	return nil
}

type OpportunityDiscoveryReportCandidate struct {
	Symbol                  string                `json:"symbol"`
	Market                  string                `json:"market,omitempty"`
	Name                    string                `json:"name,omitempty"`
	InstrumentType          string                `json:"instrument_type,omitempty"`
	RelationType            string                `json:"relation_type,omitempty"`
	Rank                    int                   `json:"rank,omitempty"`
	RelevanceScore          float64               `json:"relevance_score"`
	EvidenceScore           float64               `json:"evidence_score"`
	MarketRiskScore         float64               `json:"market_risk_score"`
	Confidence              float64               `json:"confidence"`
	Reason                  string                `json:"reason,omitempty"`
	RiskSummary             string                `json:"risk_summary,omitempty"`
	SuggestedStrategyIntent string                `json:"suggested_strategy_intent,omitempty"`
	HorizonOutlooks         []ModelHorizonOutlook `json:"horizon_outlooks"`
}

type OpportunityDiscoveryReportExclusion struct {
	Symbol string `json:"symbol,omitempty"`
	Reason string `json:"reason,omitempty"`
}

type OpportunityDiscoveryContext struct {
	BuiltAt          time.Time                        `json:"builtAt"`
	Mode             string                           `json:"mode"`
	MarketScanRunID  string                           `json:"marketScanRunId,omitempty"`
	MarketCandidates []OpportunityMarketScanCandidate `json:"marketCandidates,omitempty"`
	Opportunity      Opportunity                      `json:"opportunity"`
	DiscoveryRun     OpportunityDiscoveryRun          `json:"discoveryRun"`
	Steps            []OpportunityDiscoveryStep       `json:"steps,omitempty"`
	EmbeddingStatus  EmbeddingStatus                  `json:"embeddingStatus,omitempty"`
	FreshnessSummary map[string]any                   `json:"freshnessSummary,omitempty"`
}

type OpportunityListFilter struct {
	Status          string
	MarketScope     string
	InstrumentScope string
	Keyword         string
	Limit           int
	Offset          int
}

type DiscoveryRunListFilter struct {
	OpportunityID string
	Status        string
	Limit         int
	Offset        int
}

type DiscoveryStepListFilter struct {
	RunID  string
	Status string
	Limit  int
	Offset int
}

type OpportunityEvidenceListFilter struct {
	RunID       string
	CandidateID string
	SourceType  string
	Limit       int
	Offset      int
}

type OpportunityCandidateListFilter struct {
	OpportunityID string
	RunID         string
	Symbol        string
	Status        string
	Limit         int
	Offset        int
}

type EmbeddingAssetListFilter struct {
	ObjectType string
	ObjectID   string
	ModelID    string
	Dimensions int
	Status     string
	Limit      int
	Offset     int
}

type SemanticSearchRequest struct {
	Query    string  `json:"query"`
	Limit    int     `json:"limit,omitempty"`
	MinScore float64 `json:"minScore,omitempty"`
	AsOf     string  `json:"asOf,omitempty"`
}

type SemanticStockProfileResult struct {
	Score   float64        `json:"score"`
	Profile StockProfile   `json:"profile"`
	Asset   EmbeddingAsset `json:"asset"`
}

type SemanticNewsEventResult struct {
	Score float64        `json:"score"`
	Event NewsEvent      `json:"event"`
	Asset EmbeddingAsset `json:"asset"`
}

type SemanticNewsThreadResult struct {
	Score              float64            `json:"score"`
	Thread             NewsThread         `json:"thread"`
	Version            *NewsThreadVersion `json:"version,omitempty"`
	RetrievalVersionID string             `json:"retrievalVersionId,omitempty"`
	Asset              EmbeddingAsset     `json:"asset"`
}

func validOpportunityStatus(v string) bool {
	return v == OpportunityStatusDraft || v == OpportunityStatusResearching || v == OpportunityStatusCompleted || v == OpportunityStatusClosed
}

func validOpportunityMarketScope(v string) bool {
	return v == OpportunityMarketScopeAShare || v == OpportunityMarketScopeHK || v == OpportunityMarketScopeUS || v == OpportunityMarketScopeAll
}

func validOpportunityInstrumentScope(v string) bool {
	return v == OpportunityInstrumentScopeStock || v == OpportunityInstrumentScopeExchangeFund || v == OpportunityInstrumentScopeBoth
}

func validOpportunityRunStatus(v string) bool {
	return v == OpportunityDiscoveryRunStatusPending || v == OpportunityDiscoveryRunStatusRunning || v == OpportunityDiscoveryRunStatusCompleted || v == OpportunityDiscoveryRunStatusFailed || v == OpportunityDiscoveryRunStatusCancelled
}

func validOpportunityStepStatus(v string) bool {
	return v == OpportunityDiscoveryStepStatusPending || v == OpportunityDiscoveryStepStatusRunning || v == OpportunityDiscoveryStepStatusCompleted || v == OpportunityDiscoveryStepStatusFailed
}

func validOpportunityCandidateStatus(v string) bool {
	return v == OpportunityCandidateStatusCandidate || v == OpportunityCandidateStatusShortlisted || v == OpportunityCandidateStatusRejected || v == OpportunityCandidateStatusStrategyRequested || v == OpportunityCandidateStatusStrategyGenerated
}

func validOpportunityRelationType(v string) bool {
	return v == OpportunityRelationDirect || v == OpportunityRelationSupplyChain || v == OpportunityRelationThemeETF || v == OpportunityRelationCompetitor || v == OpportunityRelationWeak
}

func validEmbeddingAssetStatus(v string) bool {
	return v == EmbeddingAssetStatusReady || v == EmbeddingAssetStatusStale || v == EmbeddingAssetStatusFailed
}
