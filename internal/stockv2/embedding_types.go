package stockv2

import (
	"errors"
	"time"
)

const (
	EmbeddingConfigDefaultID = "default"

	EmbeddingObjectStockProfile = "stock_profile"
	EmbeddingObjectNewsEvent    = "news_event"
	EmbeddingObjectOpportunity  = "opportunity"
	EmbeddingObjectTheme        = "theme"

	EmbeddingAssetStatusReady  = "ready"
	EmbeddingAssetStatusStale  = "stale"
	EmbeddingAssetStatusFailed = "failed"
)

var (
	ErrEmbeddingModelNotConfigured = errors.New("embedding_model_not_configured")
	ErrEmbeddingModelUnavailable   = errors.New("embedding_model_unavailable")
	ErrEmbeddingAssetNotReady      = errors.New("embedding_asset_not_ready")
	ErrInvalidEmbeddingConfig      = errors.New("invalid embedding config")
	ErrInvalidEmbeddingRequest     = errors.New("invalid embedding request")
)

type EmbeddingConfig struct {
	ID               string    `json:"id"`
	EmbeddingModelID string    `json:"embeddingModelId,omitempty"`
	Enabled          bool      `json:"enabled"`
	LastProbeAt      time.Time `json:"lastProbeAt,omitempty"`
	LastProbeStatus  string    `json:"lastProbeStatus,omitempty"`
	LastError        string    `json:"lastError,omitempty"`
	UpdatedAt        time.Time `json:"updatedAt"`
}

type EmbeddingAsset struct {
	ID                  string    `json:"id"`
	ObjectType          string    `json:"objectType"`
	ObjectID            string    `json:"objectId"`
	TextHash            string    `json:"textHash"`
	TextSummary         string    `json:"textSummary,omitempty"`
	ModelID             string    `json:"modelId"`
	ProviderID          string    `json:"providerId"`
	EmbeddingProtocol   string    `json:"embeddingProtocol"`
	EmbeddingDimensions int       `json:"embeddingDimensions"`
	VectorRef           string    `json:"vectorRef"`
	Status              string    `json:"status"`
	ErrorMessage        string    `json:"errorMessage,omitempty"`
	CreatedAt           time.Time `json:"createdAt"`
	UpdatedAt           time.Time `json:"updatedAt"`
}

type EmbeddingStatus struct {
	Available           bool            `json:"available"`
	ErrorCode           string          `json:"errorCode,omitempty"`
	ErrorMessage        string          `json:"errorMessage,omitempty"`
	Config              EmbeddingConfig `json:"config"`
	ModelID             string          `json:"modelId,omitempty"`
	ProviderID          string          `json:"providerId,omitempty"`
	ModelName           string          `json:"modelName,omitempty"`
	EmbeddingProtocol   string          `json:"embeddingProtocol,omitempty"`
	EmbeddingDimensions int             `json:"embeddingDimensions,omitempty"`
	ReadyAssetCount     int             `json:"readyAssetCount"`
	StaleAssetCount     int             `json:"staleAssetCount"`
	FailedAssetCount    int             `json:"failedAssetCount"`
}

type RequestUpdateEmbeddingConfig struct {
	EmbeddingModelID *string `json:"embeddingModelId,omitempty"`
	Enabled          *bool   `json:"enabled,omitempty"`
}

type RequestRebuildEmbeddingAssets struct {
	ObjectTypes []string `json:"objectTypes,omitempty"`
	Limit       int      `json:"limit,omitempty"`
}

type EmbeddingRebuildResult struct {
	ObjectTypes []string        `json:"objectTypes"`
	Total       int             `json:"total"`
	Success     int             `json:"success"`
	Failed      int             `json:"failed"`
	FailedItems []UpdateFailure `json:"failedItems,omitempty"`
	UpdatedAt   time.Time       `json:"updatedAt"`
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

type embeddingModelBinding struct {
	Config   EmbeddingConfig
	Model    AgentModelProfile
	Provider AgentProviderProfile
}
