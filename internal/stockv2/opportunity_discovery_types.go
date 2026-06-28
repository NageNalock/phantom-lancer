package stockv2

import (
	"errors"
	"time"
)

const (
	OpportunityStatusDraft       = "draft"
	OpportunityStatusResearching = "researching"
	OpportunityStatusCompleted   = "completed"
	OpportunityStatusClosed      = "closed"
)

const (
	OpportunityDiscoveryRunStatusPending   = "pending"
	OpportunityDiscoveryRunStatusRunning   = "running"
	OpportunityDiscoveryRunStatusCompleted = "completed"
	OpportunityDiscoveryRunStatusFailed    = "failed"
	OpportunityDiscoveryRunStatusCancelled = "cancelled"
)

const (
	OpportunityDiscoveryStepStatusPending   = "pending"
	OpportunityDiscoveryStepStatusRunning   = "running"
	OpportunityDiscoveryStepStatusCompleted = "completed"
	OpportunityDiscoveryStepStatusFailed    = "failed"
)

const (
	OpportunityEvidenceSourceExternal = "external_source"
	OpportunityEvidenceSourceAgent    = "agent_note"
)

const (
	OpportunityCandidateStatusCandidate         = "candidate"
	OpportunityCandidateStatusShortlisted       = "shortlisted"
	OpportunityCandidateStatusRejected          = "rejected"
	OpportunityCandidateStatusStrategyRequested = "strategy_requested"
	OpportunityCandidateStatusStrategyGenerated = "strategy_generated"
)

const OpportunityDiscoveryReportSchemaVersion = "opportunity-discovery-report/v1"
const OpportunityDiscoveryOutputType = "opportunity_discovery"

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
	ID                  string    `json:"id"`
	OpportunityID       string    `json:"opportunityId"`
	AgentRunID          string    `json:"agentRunId,omitempty"`
	Status              string    `json:"status"`
	CurrentStepID       string    `json:"currentStepId,omitempty"`
	StepTotal           int       `json:"stepTotal"`
	StepCompleted       int       `json:"stepCompleted"`
	CandidateCount      int       `json:"candidateCount"`
	EvidenceCount       int       `json:"evidenceCount"`
	ExternalSourceCount int       `json:"externalSourceCount"`
	StartedAt           time.Time `json:"startedAt,omitempty"`
	FinishedAt          time.Time `json:"finishedAt,omitempty"`
	ErrorMessage        string    `json:"errorMessage,omitempty"`
	CreatedAt           time.Time `json:"createdAt"`
	UpdatedAt           time.Time `json:"updatedAt"`
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
	ID              string         `json:"id"`
	OpportunityID   string         `json:"opportunityId"`
	RunID           string         `json:"runId"`
	Symbol          string         `json:"symbol"`
	Market          string         `json:"market"`
	InstrumentType  string         `json:"instrumentType"`
	Name            string         `json:"name"`
	RelationType    string         `json:"relationType"`
	RelevanceScore  float64        `json:"relevanceScore"`
	EvidenceScore   float64        `json:"evidenceScore"`
	MarketRiskScore float64        `json:"marketRiskScore"`
	Confidence      float64        `json:"confidence"`
	Rank            int            `json:"rank"`
	Status          string         `json:"status"`
	Reason          string         `json:"reason,omitempty"`
	RiskSummary     string         `json:"riskSummary,omitempty"`
	Metadata        map[string]any `json:"metadata,omitempty"`
	CreatedAt       time.Time      `json:"createdAt"`
	UpdatedAt       time.Time      `json:"updatedAt"`
}

type OpportunityDiscoveryResult struct {
	ID                    string         `json:"id"`
	RunID                 string         `json:"runId"`
	Summary               string         `json:"summary,omitempty"`
	Conclusion            string         `json:"conclusion,omitempty"`
	RecommendedNextAction string         `json:"recommendedNextAction,omitempty"`
	RawResult             map[string]any `json:"rawResult,omitempty"`
	CreatedAt             time.Time      `json:"createdAt"`
}

type OpportunityDiscoveryInput struct {
	OpportunityID string `json:"opportunityId"`
	RequestedBy   string `json:"requestedBy,omitempty"`
	Async         bool   `json:"async,omitempty"`
}

type OpportunityDiscoveryContext struct {
	BuiltAt          time.Time                 `json:"builtAt"`
	Input            OpportunityDiscoveryInput `json:"input"`
	Opportunity      Opportunity               `json:"opportunity"`
	DiscoveryRun     OpportunityDiscoveryRun   `json:"discoveryRun"`
	EmbeddingStatus  map[string]any            `json:"embeddingStatus,omitempty"`
	FreshnessSummary map[string]any            `json:"freshnessSummary,omitempty"`
}

type EmbeddingConfig struct {
	ID               string    `json:"id"`
	EmbeddingModelID string    `json:"embeddingModelId,omitempty"`
	Enabled          bool      `json:"enabled"`
	LastProbeAt      time.Time `json:"lastProbeAt,omitempty"`
	LastProbeStatus  string    `json:"lastProbeStatus,omitempty"`
	LastError        string    `json:"lastError,omitempty"`
	UpdatedAt        time.Time `json:"updatedAt"`
}

var (
	ErrOpportunityNotFound              = errors.New("opportunity not found")
	ErrOpportunityDiscoveryRunNotFound  = errors.New("opportunity discovery run not found")
	ErrOpportunityDiscoveryStepNotFound = errors.New("opportunity discovery step not found")
	ErrOpportunityCandidateNotFound     = errors.New("opportunity candidate not found")
)
