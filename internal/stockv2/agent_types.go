package stockv2

import (
	"errors"
	"time"
)

// Agent 治理层:股票 V2 自己的 Provider/Model 管理、任务绑定、
// 运行记录与决策留痕。不耦合 Codex 页面。本轮不真实调用外部模型,
// 不写假 AI 结论;AgentRun 停在 ready,Output 留空,等待后续轮次接入
// 真实 executor。脱敏单点在 service 层(经 internal/safelog),store
// 只持久化,HTTP handler 不直接构造敏感字段。

// ===== Provider 类型 =====

const (
	AgentProviderTypeOpenAI   = "openai"
	AgentProviderTypeCodexCLI = "codex_cli"
	AgentProviderTypeLocal    = "local"
)

// Provider 配置状态:是否已完成必要配置。
const (
	AgentProviderConfigStateNotConfigured = "not_configured"
	AgentProviderConfigStateConfigured    = "configured"
	AgentProviderConfigStateMisconfigured = "misconfigured"
)

// Provider 认证状态。
const (
	AgentProviderAuthStateUnauthenticated = "unauthenticated"
	AgentProviderAuthStateAuthenticated   = "authenticated"
	AgentProviderAuthStateExpired         = "expired"
	AgentProviderAuthStateUnknown         = "unknown"
)

// Provider 可用性(最近探测结果)。
const (
	AgentProviderAvailabilityUnknown     = "unknown"
	AgentProviderAvailabilityAvailable   = "available"
	AgentProviderAvailabilityUnavailable = "unavailable"
	AgentProviderAvailabilityDegraded    = "degraded"
)

// Model 状态。Enabled 是独立开关;Enabled=false 时直接跳过,不再看 Status。
const (
	AgentModelStatusAvailable   = "available"
	AgentModelStatusDegraded    = "degraded"
	AgentModelStatusUnavailable = "unavailable"
)

// Model 成本等级。
const (
	AgentModelCostLevelLow    = "low"
	AgentModelCostLevelMedium = "medium"
	AgentModelCostLevelHigh   = "high"
)

// Agent 任务类型。本轮只内置 operation_review;其余 task type 校验拒绝。
const (
	AgentTaskTypeOperationReview = "operation_review"
)

// AgentRun 状态机。本轮 CreateAgentRunRecord 只产出 ready;
// running/completed/failed 为后续真实调用轮次预留,本轮不推进。
const (
	AgentRunStatusPending   = "pending"
	AgentRunStatusReady     = "ready"
	AgentRunStatusRunning   = "running"
	AgentRunStatusCompleted = "completed"
	AgentRunStatusFailed    = "failed"
)

// ResolveAgentTask 解析结果状态。
const (
	AgentResolutionStatusAuthorized = "authorized"
)

// operation_review 默认 task profile 的固定 seed id。
// schema 内 INSERT OR IGNORE 幂等种入,模型绑定留空,由用户后续绑定。
const agentTaskOperationReviewSeedID = "agent-task-operation-review"

var (
	ErrAgentProviderNotFound            = errors.New("agent provider profile not found")
	ErrAgentModelNotFound               = errors.New("agent model profile not found")
	ErrAgentTaskProfileNotFound         = errors.New("agent task profile not found")
	ErrAgentRunNotFound                 = errors.New("agent run not found")
	ErrAgentDecisionLedgerNotFound      = errors.New("agent decision ledger not found")
	ErrAgentModelNotAvailable           = errors.New("no available agent model for task")
	ErrAgentExecutorUnavailable         = errors.New("agent executor unavailable")
	ErrInvalidAgentProviderType         = errors.New("invalid agent provider type")
	ErrInvalidAgentProviderConfigState  = errors.New("invalid agent provider config state")
	ErrInvalidAgentProviderAuthState    = errors.New("invalid agent provider auth state")
	ErrInvalidAgentProviderAvailability = errors.New("invalid agent provider availability")
	ErrInvalidAgentProviderName         = errors.New("agent provider name is required")
	ErrAgentProviderAPIKeyRequired      = errors.New("agent provider api key is required")
	ErrAgentProviderBaseURLRequired     = errors.New("agent provider base url is required")
	ErrInvalidAgentModelStatus          = errors.New("invalid agent model status")
	ErrInvalidAgentModelCostLevel       = errors.New("invalid agent model cost level")
	ErrInvalidAgentModelName            = errors.New("agent model name is required")
	ErrInvalidAgentTaskType             = errors.New("invalid agent task type")
)

// AgentProviderProfile 供应商层(openai/codex_cli/local)。
// API Key 存在 metadata 内部字段,对外响应只返回 APIKeySet,不回显 secret。
type AgentProviderProfile struct {
	ID              string         `json:"id"`
	ProviderType    string         `json:"providerType"`
	Name            string         `json:"name"`
	DisplayName     string         `json:"displayName,omitempty"`
	BaseURL         string         `json:"baseUrl,omitempty"`
	APIKeySet       bool           `json:"apiKeySet,omitempty"`
	ConfigState     string         `json:"configState"`
	AuthState       string         `json:"authState"`
	Availability    string         `json:"availability"`
	LastProbeAt     time.Time      `json:"lastProbeAt,omitempty"`
	LastProbeResult string         `json:"lastProbeResult,omitempty"` // 已脱敏摘要
	Metadata        map[string]any `json:"metadata,omitempty"`
	CreatedAt       time.Time      `json:"createdAt"`
	UpdatedAt       time.Time      `json:"updatedAt"`
}

// AgentModelProfile 具体模型配置。
type AgentModelProfile struct {
	ID           string         `json:"id"`
	ProviderID   string         `json:"providerId"`
	ModelName    string         `json:"modelName"`
	DisplayName  string         `json:"displayName,omitempty"`
	Enabled      bool           `json:"enabled"`
	Status       string         `json:"status"`
	CostLevel    string         `json:"costLevel"`
	ContextLimit int            `json:"contextLimit"`
	Metadata     map[string]any `json:"metadata,omitempty"`
	CreatedAt    time.Time      `json:"createdAt"`
	UpdatedAt    time.Time      `json:"updatedAt"`
}

// AgentTaskProfile 股票任务到模型的绑定。operation_review 由 schema 默认种入。
type AgentTaskProfile struct {
	ID              string    `json:"id"`
	TaskType        string    `json:"taskType"`
	PrimaryModelID  string    `json:"primaryModelId,omitempty"`
	FallbackModelID string    `json:"fallbackModelId,omitempty"`
	MaxBudget       int       `json:"maxBudget,omitempty"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

// AgentRun 一次 Agent 任务运行记录。
// 本轮不真实调用模型,Status 停在 ready,Output 留空(不写假结论)。
type AgentRun struct {
	ID                string         `json:"id"`
	TaskType          string         `json:"taskType"`
	ProviderID        string         `json:"providerId,omitempty"`
	ModelID           string         `json:"modelId,omitempty"`
	TriggerObjectType string         `json:"triggerObjectType"`
	TriggerObjectID   string         `json:"triggerObjectId"`
	Status            string         `json:"status"`
	CostEstimate      map[string]any `json:"costEstimate,omitempty"` // 本轮空 {}
	ErrorMessage      string         `json:"errorMessage,omitempty"` // 已脱敏
	Output            string         `json:"output,omitempty"`       // 本轮空串(不写假结论)
	DecisionLedgerID  string         `json:"decisionLedgerId,omitempty"`
	StartedAt         time.Time      `json:"startedAt,omitempty"`
	FinishedAt        time.Time      `json:"finishedAt,omitempty"`
	CreatedAt         time.Time      `json:"createdAt"`
	UpdatedAt         time.Time      `json:"updatedAt"`
}

// AgentDecisionLedger 决策留痕。写入前经 safelog 脱敏裁剪,不记录 secret。
type AgentDecisionLedger struct {
	ID                    string         `json:"id"`
	RunID                 string         `json:"runId,omitempty"`
	ProviderID            string         `json:"providerId,omitempty"`
	ModelID               string         `json:"modelId,omitempty"`
	TaskType              string         `json:"taskType"`
	TriggerObjectType     string         `json:"triggerObjectType"`
	TriggerObjectID       string         `json:"triggerObjectId"`
	InputSummary          string         `json:"inputSummary,omitempty"`          // 已脱敏
	Prompt                string         `json:"prompt,omitempty"`                // 已脱敏
	InputArtifactSummary  string         `json:"inputArtifactSummary,omitempty"`  // 已脱敏
	OutputArtifactSummary string         `json:"outputArtifactSummary,omitempty"` // 本轮空
	StructuredOutput      map[string]any `json:"structuredOutput,omitempty"`      // 本轮空 {}
	RedactionSummary      map[string]any `json:"redactionSummary,omitempty"`
	CreatedAt             time.Time      `json:"createdAt"`
	UpdatedAt             time.Time      `json:"updatedAt"`
}

// ===== 请求 DTO =====

type RequestCreateAgentProviderProfile struct {
	ProviderType string         `json:"providerType"`
	Name         string         `json:"name"`
	DisplayName  string         `json:"displayName,omitempty"`
	BaseURL      string         `json:"baseUrl,omitempty"`
	APIKey       string         `json:"apiKey,omitempty"`
	ConfigState  string         `json:"configState,omitempty"`
	AuthState    string         `json:"authState,omitempty"`
	Availability string         `json:"availability,omitempty"`
	Metadata     map[string]any `json:"metadata,omitempty"`
}

type RequestUpdateAgentProviderProfile struct {
	Name            *string        `json:"name,omitempty"`
	DisplayName     *string        `json:"displayName,omitempty"`
	BaseURL         *string        `json:"baseUrl,omitempty"`
	APIKey          *string        `json:"apiKey,omitempty"`
	ConfigState     *string        `json:"configState,omitempty"`
	AuthState       *string        `json:"authState,omitempty"`
	Availability    *string        `json:"availability,omitempty"`
	LastProbeResult *string        `json:"lastProbeResult,omitempty"`
	Metadata        map[string]any `json:"metadata,omitempty"` // 整体替换
}

type RequestCreateAgentModelProfile struct {
	ProviderID   string         `json:"providerId"`
	ModelName    string         `json:"modelName"`
	DisplayName  string         `json:"displayName,omitempty"`
	Enabled      bool           `json:"enabled"`
	Status       string         `json:"status,omitempty"`
	CostLevel    string         `json:"costLevel,omitempty"`
	ContextLimit int            `json:"contextLimit,omitempty"`
	Metadata     map[string]any `json:"metadata,omitempty"`
}

type RequestUpdateAgentModelProfile struct {
	DisplayName  *string        `json:"displayName,omitempty"`
	Enabled      *bool          `json:"enabled,omitempty"`
	Status       *string        `json:"status,omitempty"`
	CostLevel    *string        `json:"costLevel,omitempty"`
	ContextLimit *int           `json:"contextLimit,omitempty"`
	Metadata     map[string]any `json:"metadata,omitempty"`
}

type AgentProviderModelCatalogItem struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName,omitempty"`
}

type AgentProviderModelCatalog struct {
	ProviderID string                          `json:"providerId"`
	Items      []AgentProviderModelCatalogItem `json:"items"`
}

type RequestTestAgentModel struct {
	ProviderID string `json:"providerId"`
	ModelName  string `json:"modelName"`
}

type AgentModelTestResult struct {
	OK        bool   `json:"ok"`
	Message   string `json:"message,omitempty"`
	LatencyMS int64  `json:"latencyMs,omitempty"`
}

type RequestUpdateAgentTaskProfile struct {
	PrimaryModelID  *string `json:"primaryModelId,omitempty"`
	FallbackModelID *string `json:"fallbackModelId,omitempty"`
	MaxBudget       *int    `json:"maxBudget,omitempty"`
}

type RequestResolveAgentTask struct {
	TaskType          string `json:"taskType"`
	TriggerObjectType string `json:"triggerObjectType"`
	TriggerObjectID   string `json:"triggerObjectId"`
	RequestedBy       string `json:"requestedBy,omitempty"`
}

type RequestRunAgentCLIDebug struct {
	ModelID     string `json:"modelId"`
	RequestedBy string `json:"requestedBy,omitempty"`
}

type AgentExecutionDetail struct {
	Run          AgentRun             `json:"run"`
	Ledger       *AgentDecisionLedger `json:"ledger,omitempty"`
	Review       *OperationReview     `json:"review,omitempty"`
	InputContext *AgentContextPack    `json:"inputContext,omitempty"`
}

// ===== 内部参数 / 返回 struct =====

// AgentRunRecordParams CreateAgentRunRecord 的入参;
// 敏感字段由 service 脱敏后落库,store 不重复脱敏。
type AgentRunRecordParams struct {
	TaskType             string
	ProviderID           string
	ModelID              string
	TriggerObjectType    string
	TriggerObjectID      string
	RequestedBy          string
	InputSummary         string
	Prompt               string
	InputArtifactSummary string
}

// AgentTaskResolution ResolveAgentTask 的返回。
// Run 与 DecisionLedger 非 nil。
type AgentTaskResolution struct {
	TaskType          string               `json:"taskType"`
	TaskProfileID     string               `json:"taskProfileId,omitempty"`
	ProviderID        string               `json:"providerId,omitempty"`
	ModelID           string               `json:"modelId,omitempty"`
	ModelName         string               `json:"modelName,omitempty"`
	TriggerObjectType string               `json:"triggerObjectType"`
	TriggerObjectID   string               `json:"triggerObjectId"`
	RequestedBy       string               `json:"requestedBy,omitempty"`
	Status            string               `json:"status"`
	Run               *AgentRun            `json:"run,omitempty"`
	DecisionLedger    *AgentDecisionLedger `json:"decisionLedger,omitempty"`
}

// ===== ListFilter(不序列化,仅内部传递) =====

type AgentProviderProfileListFilter struct {
	ProviderType string
	ConfigState  string
	AuthState    string
	Availability string
	Limit        int
	Offset       int
}

type AgentModelProfileListFilter struct {
	ProviderID string
	Status     string
	Enabled    *bool // nil=不过滤; true/false 精确过滤
	CostLevel  string
	Limit      int
	Offset     int
}

type AgentTaskProfileListFilter struct {
	TaskType string
	Limit    int
	Offset   int
}

type AgentRunListFilter struct {
	TaskType          string
	Status            string
	ProviderID        string
	ModelID           string
	TriggerObjectType string
	TriggerObjectID   string
	Limit             int
	Offset            int
}

// ===== 校验函数 =====

func validAgentProviderType(v string) bool {
	return v == AgentProviderTypeOpenAI ||
		v == AgentProviderTypeCodexCLI ||
		v == AgentProviderTypeLocal
}

func validAgentProviderConfigState(v string) bool {
	return v == AgentProviderConfigStateNotConfigured ||
		v == AgentProviderConfigStateConfigured ||
		v == AgentProviderConfigStateMisconfigured
}

func validAgentProviderAuthState(v string) bool {
	return v == AgentProviderAuthStateUnauthenticated ||
		v == AgentProviderAuthStateAuthenticated ||
		v == AgentProviderAuthStateExpired ||
		v == AgentProviderAuthStateUnknown
}

func validAgentProviderAvailability(v string) bool {
	return v == AgentProviderAvailabilityUnknown ||
		v == AgentProviderAvailabilityAvailable ||
		v == AgentProviderAvailabilityUnavailable ||
		v == AgentProviderAvailabilityDegraded
}

func validAgentModelStatus(v string) bool {
	return v == AgentModelStatusAvailable ||
		v == AgentModelStatusDegraded ||
		v == AgentModelStatusUnavailable
}

func validAgentModelCostLevel(v string) bool {
	return v == AgentModelCostLevelLow ||
		v == AgentModelCostLevelMedium ||
		v == AgentModelCostLevelHigh
}

func validAgentTaskType(v string) bool {
	return v == AgentTaskTypeOperationReview
}
