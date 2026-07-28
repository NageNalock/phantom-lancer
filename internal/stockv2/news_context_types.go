package stockv2

import (
	"errors"
	"strings"
	"time"
)

const (
	NewsContextConfigIDDefault = "stockv2-news-context-default"

	NewsContextWindowHourly   = "hourly"
	NewsContextWindowFourHour = "four_hour"
	NewsContextWindowDaily    = "daily"

	NewsContextTriggerScheduled = "scheduled"
	NewsContextTriggerManual    = "manual"
	NewsContextTriggerRetry     = "retry"

	NewsContextRunStatusPending       = "pending"
	NewsContextRunStatusRunning       = "running"
	NewsContextRunStatusWaitingReview = "waiting_review"
	NewsContextRunStatusCompleted     = "completed"
	NewsContextRunStatusFailed        = "failed"

	NewsContextReviewNotRequired = "not_required"
	NewsContextReviewPending     = "pending"
	NewsContextReviewRunning     = "running"
	NewsContextReviewCompleted   = "completed"
	NewsContextReviewFailed      = "failed"

	NewsContextIndexPending = "pending"
	NewsContextIndexReady   = "ready"
	NewsContextIndexStale   = "stale"
	NewsContextIndexFailed  = "failed"

	NewsContextResearchNotRequired = "not_required"
	NewsContextResearchCompleted   = "completed"
	NewsContextResearchFailed      = "failed"
	NewsContextResearchUnavailable = "unavailable"
	NewsContextResearchUnresolved  = "unresolved"

	NewsContextCleanupPending   = "pending"
	NewsContextCleanupRunning   = "running"
	NewsContextCleanupCompleted = "completed"
	NewsContextCleanupPartial   = "partial"
	NewsContextCleanupFailed    = "failed"

	NewsContextRunItemNewsEvent = "news_event"
	NewsContextRunItemThread    = "news_thread"
	NewsContextRunItemPending   = "pending"
	NewsContextRunItemRunning   = "running"
	NewsContextRunItemCompleted = "completed"
	NewsContextRunItemDeferred  = "deferred"
	NewsContextRunItemFailed    = "failed"

	NewsEventContextPending   = "pending"
	NewsEventContextCovered   = "covered"
	NewsEventContextNoise     = "noise"
	NewsEventContextDeferred  = "deferred"
	NewsEventContextCompacted = "compacted"

	NewsThreadStatusActive   = "active"
	NewsThreadStatusDormant  = "dormant"
	NewsThreadStatusMerged   = "merged"
	NewsThreadStatusArchived = "archived"

	NewsThreadStageEmerging     = "emerging"
	NewsThreadStageSpreading    = "spreading"
	NewsThreadStageAccelerating = "accelerating"
	NewsThreadStageOverheated   = "overheated"
	NewsThreadStageDiverging    = "diverging"
	NewsThreadStageRetreating   = "retreating"
	NewsThreadStageDormant      = "dormant"
	NewsThreadStageRestarting   = "restarting"

	NewsContextResultSchemaVersion = "news-context-result/v1"
	NewsContextOutputType          = "news_context_result"
)

func normalizeNewsContextDisposition(value string) (string, bool) {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "create", "update", "support", "contradict", "background", "duplicate", "noise":
		return value, true
	case "defer", NewsEventContextDeferred:
		return NewsEventContextDeferred, true
	default:
		return "", false
	}
}

var (
	ErrNewsThreadNotFound          = errors.New("news thread not found")
	ErrNewsContextRunNotFound      = errors.New("news context run not found")
	ErrNewsContextCleanupNotFound  = errors.New("news context cleanup run not found")
	ErrInvalidNewsContextInput     = errors.New("invalid news context input")
	ErrInvalidNewsContextResult    = errors.New("invalid news context result")
	ErrNewsContextAlreadyRunning   = errors.New("news context aggregation already running")
	ErrNewsContextCleanupRunning   = errors.New("news context cleanup already running")
	ErrNewsContextFeatureDisabled  = errors.New("news context scheduling is disabled")
	ErrNewsContextCleanupDisabled  = errors.New("news context automatic cleanup is disabled")
	ErrNewsContextReviewIncomplete = errors.New("news context review is incomplete")
	ErrNewsContextPrerequisite     = errors.New("news context prerequisite is unavailable")
)

type NewsThreadRelation struct {
	ThreadID string  `json:"targetThemeId,omitempty"`
	Title    string  `json:"targetThemeTitle,omitempty"`
	Type     string  `json:"relationType"`
	Reason   string  `json:"summary,omitempty"`
	Strength float64 `json:"strength,omitempty"`
}

type NewsThread struct {
	ID                  string               `json:"id"`
	ThemeID             string               `json:"themeId,omitempty"`
	Title               string               `json:"title"`
	Summary             string               `json:"summary,omitempty"`
	CoreThesis          string               `json:"coreThesis"`
	Stage               string               `json:"stage"`
	LatestChange        string               `json:"latestChange,omitempty"`
	Confidence          float64              `json:"confidence"`
	Status              string               `json:"status"`
	Industries          []string             `json:"industries,omitempty"`
	Symbols             []string             `json:"symbols,omitempty"`
	Funds               []string             `json:"funds,omitempty"`
	Facts               []string             `json:"confirmedFacts,omitempty"`
	Inferences          []string             `json:"inferences,omitempty"`
	CounterEvidence     []string             `json:"counterEvidence,omitempty"`
	OpenQuestions       []string             `json:"openQuestions,omitempty"`
	Leaders             []string             `json:"leaders,omitempty"`
	Followers           []string             `json:"followers,omitempty"`
	Laggards            []string             `json:"laggards,omitempty"`
	NextCandidates      []string             `json:"relayCandidates,omitempty"`
	Catalysts           []string             `json:"catalysts,omitempty"`
	Invalidations       []string             `json:"invalidationConditions,omitempty"`
	Relations           []NewsThreadRelation `json:"relations,omitempty"`
	CurrentVersion      int                  `json:"currentVersion"`
	CurrentVersionID    string               `json:"currentVersionId,omitempty"`
	ReviewStatus        string               `json:"reviewStatus"`
	IndexStatus         string               `json:"indexStatus"`
	IndexError          string               `json:"indexError,omitempty"`
	DataConfirmation    string               `json:"dataConfirmation,omitempty"`
	ConfirmationSignals []string             `json:"confirmationSignals,omitempty"`
	InvalidationSignals []string             `json:"invalidationSignals,omitempty"`
	FirstSeenAt         time.Time            `json:"firstSeenAt"`
	LastChangedAt       time.Time            `json:"lastChangedAt"`
	LastReviewedAt      time.Time            `json:"lastReviewedAt,omitempty"`
	CreatedAt           time.Time            `json:"createdAt"`
	UpdatedAt           time.Time            `json:"updatedAt"`
}

type NewsThreadVersion struct {
	ID              string               `json:"id"`
	ThreadID        string               `json:"themeId"`
	RunID           string               `json:"runId"`
	AgentRunID      string               `json:"agentRunId,omitempty"`
	WindowType      string               `json:"windowType"`
	VersionNo       int                  `json:"versionNo"`
	Title           string               `json:"title"`
	CoreThesis      string               `json:"conclusion"`
	Stage           string               `json:"stage"`
	LatestChange    string               `json:"changeSummary,omitempty"`
	MaterialChange  bool                 `json:"materialChange"`
	Confidence      float64              `json:"confidence"`
	Industries      []string             `json:"industries,omitempty"`
	Symbols         []string             `json:"symbols,omitempty"`
	Funds           []string             `json:"funds,omitempty"`
	Facts           []string             `json:"facts,omitempty"`
	Inferences      []string             `json:"inferences,omitempty"`
	CounterEvidence []string             `json:"counterEvidence,omitempty"`
	OpenQuestions   []string             `json:"openQuestions,omitempty"`
	Leaders         []string             `json:"leaders,omitempty"`
	Followers       []string             `json:"followers,omitempty"`
	Laggards        []string             `json:"laggards,omitempty"`
	NextCandidates  []string             `json:"nextCandidates,omitempty"`
	Catalysts       []string             `json:"catalysts,omitempty"`
	Invalidations   []string             `json:"invalidations,omitempty"`
	Relations       []NewsThreadRelation `json:"relations,omitempty"`
	ResearchStatus  string               `json:"researchStatus,omitempty"`
	EvidenceCount   int                  `json:"evidenceCount"`
	ReviewStatus    string               `json:"reviewStatus"`
	IndexStatus     string               `json:"indexStatus"`
	IndexError      string               `json:"indexError,omitempty"`
	EffectiveAt     time.Time            `json:"effectiveAt"`
	CreatedAt       time.Time            `json:"createdAt"`
}

type NewsThreadEvidence struct {
	ID                  string    `json:"id"`
	ThreadID            string    `json:"themeId"`
	VersionID           string    `json:"themeVersionId"`
	RunID               string    `json:"runId"`
	NewsEventID         string    `json:"newsEventId,omitempty"`
	Source              string    `json:"source,omitempty"`
	Title               string    `json:"title"`
	Summary             string    `json:"summary,omitempty"`
	URL                 string    `json:"url,omitempty"`
	ContentHash         string    `json:"contentHash,omitempty"`
	Relation            string    `json:"evidenceRole,omitempty"`
	EventAt             time.Time `json:"publishedAt,omitempty"`
	OriginalNewsDeleted *bool     `json:"originalNewsDeleted,omitempty"`
	CreatedAt           time.Time `json:"createdAt"`
}

type NewsThreadDetail struct {
	Theme            NewsThread                  `json:"theme"`
	Versions         []NewsThreadVersion         `json:"versions"`
	Evidence         []NewsThreadEvidence        `json:"evidence"`
	IndexStatus      string                      `json:"indexStatus"`
	IndexError       string                      `json:"indexError,omitempty"`
	MCPReadable      bool                        `json:"mcpReadable"`
	MCPVerified      bool                        `json:"mcpVerified"`
	MCPVerification  *NewsContextMCPVerification `json:"mcpVerification,omitempty"`
	MCPError         string                      `json:"mcpError,omitempty"`
	ProtectedReasons []string                    `json:"protectedReasons,omitempty"`
}

type NewsThreadChange struct {
	RunID          string    `json:"runId"`
	ThreadID       string    `json:"threadId"`
	VersionID      string    `json:"versionId"`
	Title          string    `json:"title"`
	Stage          string    `json:"stage"`
	LatestChange   string    `json:"latestChange,omitempty"`
	MaterialChange bool      `json:"materialChange"`
	CreatedAt      time.Time `json:"createdAt"`
}

type NewsThreadListFilter struct {
	ID           string
	Status       string
	Stage        string
	ReviewStatus string
	IndexStatus  string
	Query        string
	Affected     string
	Since        time.Time
	Until        time.Time
	Limit        int
	Offset       int
}

type NewsThreadVersionListFilter struct {
	ID             string
	ThreadID       string
	RunID          string
	AgentRunID     string
	WindowType     string
	ReviewStatus   string
	IndexStatus    string
	MaterialChange *bool
	Since          time.Time
	Until          time.Time
	Limit          int
	Offset         int
}

type NewsThreadEvidenceListFilter struct {
	ThreadID    string
	VersionID   string
	RunID       string
	NewsEventID string
	Limit       int
	Offset      int
}

type NewsContextConfig struct {
	ID                       string    `json:"id"`
	Enabled                  bool      `json:"enabled"`
	AutoCleanupEnabled       bool      `json:"autoCleanupEnabled"`
	HourlyEnabled            bool      `json:"hourlyEnabled"`
	FourHourEnabled          bool      `json:"fourHourEnabled"`
	DailyEnabled             bool      `json:"dailyEnabled"`
	HourlyIntervalSeconds    int       `json:"hourlyIntervalSeconds"`
	FourHourIntervalSeconds  int       `json:"fourHourIntervalSeconds"`
	DailyIntervalSeconds     int       `json:"dailyIntervalSeconds"`
	CleanupGraceSeconds      int       `json:"cleanupGraceSeconds"`
	AgentTimeoutSeconds      int       `json:"agentTimeoutSeconds"`
	TimeoutRetryLimit        int       `json:"timeoutRetryLimit"`
	RetryBackoffSeconds      int       `json:"retryBackoffSeconds"`
	ReviewTimeoutSeconds     int       `json:"reviewTimeoutSeconds"`
	SchedulerPollSeconds     int       `json:"schedulerPollSeconds"`
	AdditionalResearchPrompt string    `json:"additionalResearchPrompt,omitempty"`
	NextHourlyAt             time.Time `json:"nextHourlyAt,omitempty"`
	NextFourHourAt           time.Time `json:"nextFourHourAt,omitempty"`
	NextDailyAt              time.Time `json:"nextDailyAt,omitempty"`
	LastRunAt                time.Time `json:"lastRunAt,omitempty"`
	LastCleanupAt            time.Time `json:"lastCleanupAt,omitempty"`
	LastError                string    `json:"lastError,omitempty"`
	UpdatedAt                time.Time `json:"updatedAt"`
}

type NewsContextRun struct {
	ID                     string    `json:"id"`
	Kind                   string    `json:"kind,omitempty"`
	WindowType             string    `json:"windowType"`
	TriggerType            string    `json:"triggerType"`
	Status                 string    `json:"status"`
	Phase                  string    `json:"phase,omitempty"`
	FailedStage            string    `json:"failedStage,omitempty"`
	CoverageStatus         string    `json:"coverageStatus,omitempty"`
	Progress               float64   `json:"progress,omitempty"`
	Retryable              bool      `json:"retryable,omitempty"`
	RetryCount             int       `json:"retryCount"`
	RetryLimit             int       `json:"retryLimit"`
	NextRetryAt            time.Time `json:"nextRetryAt,omitempty,omitzero"`
	AutoRetryExhausted     bool      `json:"autoRetryExhausted,omitempty"`
	WindowStart            time.Time `json:"windowStart"`
	WindowEnd              time.Time `json:"windowEnd"`
	CurrentAgentRunID      string    `json:"currentAgentRunId,omitempty"`
	ReviewStatus           string    `json:"reviewStatus"`
	ReviewRunID            string    `json:"reviewRunId,omitempty"`
	CleanupStatus          string    `json:"cleanupStatus"`
	CleanupRunID           string    `json:"cleanupRunId,omitempty"`
	InputCount             int       `json:"totalNewsCount"`
	ProcessedCount         int       `json:"processedNewsCount"`
	CoveredCount           int       `json:"coveredCount"`
	NoiseCount             int       `json:"noiseCount"`
	DeferredCount          int       `json:"deferredCount"`
	CreatedThreadCount     int       `json:"createdThemeCount"`
	UpdatedThreadCount     int       `json:"updatedThemeCount"`
	UnchangedThreadCount   int       `json:"unchangedThemeCount,omitempty"`
	MaterialChangeCount    int       `json:"materialThemeCount"`
	ConflictCount          int       `json:"conflictThemeCount"`
	ResearchCount          int       `json:"researchCount"`
	ExternalResearchStatus string    `json:"externalResearchStatus,omitempty"`
	PendingCount           int       `json:"pendingThemeCount"`
	ErrorMessage           string    `json:"errorMessage,omitempty"`
	RequestedBy            string    `json:"requestedBy,omitempty"`
	StartedAt              time.Time `json:"startedAt,omitempty"`
	FinishedAt             time.Time `json:"finishedAt,omitempty"`
	CreatedAt              time.Time `json:"createdAt"`
	UpdatedAt              time.Time `json:"updatedAt"`
}

type NewsContextRunListFilter struct {
	WindowType   string
	TriggerType  string
	Status       string
	ReviewStatus string
	Since        time.Time
	Until        time.Time
	Limit        int
	Offset       int
}

type NewsContextRunItem struct {
	ID           string    `json:"id"`
	RunID        string    `json:"runId"`
	ObjectType   string    `json:"objectType"`
	ObjectID     string    `json:"objectId"`
	Status       string    `json:"status"`
	Disposition  string    `json:"disposition,omitempty"`
	ThreadID     string    `json:"threadId,omitempty"`
	VersionID    string    `json:"versionId,omitempty"`
	AgentRunID   string    `json:"agentRunId,omitempty"`
	ErrorMessage string    `json:"errorMessage,omitempty"`
	SourceAt     time.Time `json:"sourceAt,omitempty"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

type NewsContextRunItemListFilter struct {
	RunID      string
	ObjectType string
	Status     string
	AgentRunID string
	Limit      int
	Offset     int
}

type NewsContextCleanupRun struct {
	ID             string    `json:"id"`
	Kind           string    `json:"kind,omitempty"`
	ContextRunID   string    `json:"contextRunId,omitempty"`
	Status         string    `json:"status"`
	Phase          string    `json:"phase,omitempty"`
	FailedStage    string    `json:"failedStage,omitempty"`
	CoverageStatus string    `json:"coverageStatus,omitempty"`
	Retryable      bool      `json:"retryable,omitempty"`
	Cutoff         time.Time `json:"cutoff"`
	ScannedCount   int       `json:"processedNewsCount"`
	TotalNewsCount int       `json:"totalNewsCount,omitempty"`
	EligibleCount  int       `json:"eligibleCount"`
	CompactedCount int       `json:"deletedNewsCount"`
	ProtectedCount int       `json:"protectedNewsCount"`
	RetainedCount  int       `json:"retainedNewsCount"`
	FailedCount    int       `json:"failedCount"`
	ReleasedBytes  int64     `json:"releasedBytes"`
	ErrorMessage   string    `json:"errorMessage,omitempty"`
	RequestedBy    string    `json:"requestedBy,omitempty"`
	StartedAt      time.Time `json:"startedAt,omitempty"`
	FinishedAt     time.Time `json:"finishedAt,omitempty"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

type NewsContextCleanupRunListFilter struct {
	Status string
	Since  time.Time
	Until  time.Time
	Limit  int
	Offset int
}

type NewsContextCleanupCandidate struct {
	Event            NewsEvent `json:"event"`
	ContextStatus    string    `json:"contextStatus"`
	ContextRunID     string    `json:"contextRunId"`
	ContextCoveredAt time.Time `json:"contextCoveredAt"`
	ProtectedReason  string    `json:"protectedReason,omitempty"`
}

type RequestStartNewsContextRun struct {
	WindowType  string `json:"windowType"`
	StartAt     string `json:"startAt,omitempty"`
	EndAt       string `json:"endAt,omitempty"`
	RequestedBy string `json:"requestedBy,omitempty"`
}

type RequestStartNewsContextCleanup struct {
	ContextRunID string `json:"contextRunId,omitempty"`
	Before       string `json:"before,omitempty"`
	RequestedBy  string `json:"requestedBy,omitempty"`
}

type RequestUpdateNewsContextConfig struct {
	Enabled                  *bool   `json:"enabled,omitempty"`
	AutoCleanupEnabled       *bool   `json:"autoCleanupEnabled,omitempty"`
	HourlyEnabled            *bool   `json:"hourlyEnabled,omitempty"`
	FourHourEnabled          *bool   `json:"fourHourEnabled,omitempty"`
	DailyEnabled             *bool   `json:"dailyEnabled,omitempty"`
	CleanupGraceSeconds      *int    `json:"cleanupGraceSeconds,omitempty"`
	AdditionalResearchPrompt *string `json:"additionalResearchPrompt,omitempty"`
}

type NewsContextPromptThread struct {
	ID                  string               `json:"id"`
	ThemeID             string               `json:"themeId,omitempty"`
	RetrievalScore      float64              `json:"retrievalScore,omitempty"`
	MatchedNewsEventIDs []string             `json:"matchedNewsEventIds,omitempty"`
	Title               string               `json:"title"`
	Summary             string               `json:"summary,omitempty"`
	CoreThesis          string               `json:"coreThesis"`
	Stage               string               `json:"stage"`
	LatestChange        string               `json:"latestChange,omitempty"`
	Confidence          float64              `json:"confidence"`
	Status              string               `json:"status,omitempty"`
	Industries          []string             `json:"industries,omitempty"`
	Symbols             []string             `json:"symbols,omitempty"`
	Funds               []string             `json:"funds,omitempty"`
	Facts               []string             `json:"confirmedFacts,omitempty"`
	Inferences          []string             `json:"inferences,omitempty"`
	CounterEvidence     []string             `json:"counterEvidence,omitempty"`
	OpenQuestions       []string             `json:"openQuestions,omitempty"`
	Leaders             []string             `json:"leaders,omitempty"`
	Followers           []string             `json:"followers,omitempty"`
	Laggards            []string             `json:"laggards,omitempty"`
	NextCandidates      []string             `json:"relayCandidates,omitempty"`
	Catalysts           []string             `json:"catalysts,omitempty"`
	Invalidations       []string             `json:"invalidationConditions,omitempty"`
	Relations           []NewsThreadRelation `json:"relations,omitempty"`
	CurrentVersion      int                  `json:"currentVersion,omitempty"`
	CurrentVersionID    string               `json:"currentVersionId,omitempty"`
	EffectiveAt         string               `json:"effectiveAt,omitempty"`
	DataConfirmation    string               `json:"dataConfirmation,omitempty"`
	ConfirmationSignals []string             `json:"confirmationSignals,omitempty"`
	InvalidationSignals []string             `json:"invalidationSignals,omitempty"`
}

type NewsContextAggregationPack struct {
	RunID                    string                    `json:"runId"`
	WindowType               string                    `json:"windowType"`
	WindowStart              time.Time                 `json:"windowStart"`
	WindowEnd                time.Time                 `json:"windowEnd"`
	HistoricalReconstruction bool                      `json:"historicalReconstruction,omitempty"`
	InputNewsEvents          []NewsEvent               `json:"inputNewsEvents,omitempty"`
	InputThreads             []NewsContextPromptThread `json:"inputThreads,omitempty"`
	CandidateThreads         []NewsContextPromptThread `json:"candidateThreads,omitempty"`
	CandidateLookupStatus    string                    `json:"candidateLookupStatus,omitempty"`
	RequiredResearch         bool                      `json:"requiredResearch"`
	ResearchReasons          []string                  `json:"researchReasons,omitempty"`
	AdditionalResearchPrompt string                    `json:"additionalResearchPrompt,omitempty"`
}

type NewsContextNewsDecision struct {
	NewsEventID string `json:"news_event_id"`
	Disposition string `json:"disposition"`
	ThreadID    string `json:"thread_id,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

type NewsContextThreadChange struct {
	Action          string               `json:"action"`
	ThreadID        string               `json:"thread_id,omitempty"`
	SourceThreadIDs []string             `json:"source_thread_ids,omitempty"`
	Title           string               `json:"title"`
	CoreThesis      string               `json:"core_thesis"`
	Stage           string               `json:"stage"`
	LatestChange    string               `json:"latest_change,omitempty"`
	MaterialChange  bool                 `json:"material_change"`
	Confidence      float64              `json:"confidence"`
	Industries      []string             `json:"industries,omitempty"`
	Symbols         []string             `json:"symbols,omitempty"`
	Funds           []string             `json:"funds,omitempty"`
	Facts           []string             `json:"facts,omitempty"`
	Inferences      []string             `json:"inferences,omitempty"`
	CounterEvidence []string             `json:"counter_evidence,omitempty"`
	OpenQuestions   []string             `json:"open_questions,omitempty"`
	Leaders         []string             `json:"leaders,omitempty"`
	Followers       []string             `json:"followers,omitempty"`
	Laggards        []string             `json:"laggards,omitempty"`
	NextCandidates  []string             `json:"next_candidates,omitempty"`
	Catalysts       []string             `json:"catalysts,omitempty"`
	Invalidations   []string             `json:"invalidations,omitempty"`
	Relations       []NewsThreadRelation `json:"relations,omitempty"`
	EvidenceNewsIDs []string             `json:"evidence_news_ids,omitempty"`
	ResearchStatus  string               `json:"research_status,omitempty"`
}

type NewsContextSearchAudit struct {
	Question          string   `json:"question"`
	Status            string   `json:"status"`
	Sources           []string `json:"sources,omitempty"`
	Supported         []string `json:"supported,omitempty"`
	WeakenedOrRefuted []string `json:"weakened_or_refuted,omitempty"`
	Unresolved        []string `json:"unresolved,omitempty"`
	FailureReason     string   `json:"failure_reason,omitempty"`
}

type NewsContextReport struct {
	SchemaVersion      string                    `json:"schema_version"`
	RunID              string                    `json:"run_id"`
	WindowType         string                    `json:"window_type"`
	ProcessedNewsIDs   []string                  `json:"processed_news_ids"`
	ReviewedThreadIDs  []string                  `json:"reviewed_thread_ids"`
	UnchangedThreadIDs []string                  `json:"unchanged_thread_ids"`
	NewsDecisions      []NewsContextNewsDecision `json:"news_decisions"`
	ThreadChanges      []NewsContextThreadChange `json:"thread_changes"`
	SearchAudit        []NewsContextSearchAudit  `json:"search_audit"`
	UrgentReview       bool                      `json:"urgent_review,omitempty"`
}

type NewsContextBatchApplyResult struct {
	ProcessedCount      int      `json:"processedCount"`
	CoveredCount        int      `json:"coveredCount"`
	NoiseCount          int      `json:"noiseCount"`
	DeferredCount       int      `json:"deferredCount"`
	CreatedThreadCount  int      `json:"createdThreadCount"`
	UpdatedThreadCount  int      `json:"updatedThreadCount"`
	MaterialChangeCount int      `json:"materialChangeCount"`
	ConflictCount       int      `json:"conflictCount"`
	ChangedThreadIDs    []string `json:"changedThreadIds,omitempty"`
	ChangedVersionIDs   []string `json:"changedVersionIds,omitempty"`
	UrgentReview        bool     `json:"urgentReview,omitempty"`
}

type NewsContextRotationSignals struct {
	Mainline            []NewsThread `json:"mainThemes"`
	Accelerating        []NewsThread `json:"acceleratingThemes"`
	Retreating          []NewsThread `json:"fadingThemes"`
	NextCandidates      []NewsThread `json:"relayCandidates"`
	ConfirmationSignals []string     `json:"confirmationSignals,omitempty"`
	InvalidationSignals []string     `json:"invalidationSignals,omitempty"`
	DataStatus          string       `json:"dataStatus"`
	Summary             string       `json:"summary"`
	UpdatedAt           time.Time    `json:"updatedAt"`
}

type NewsContextSummary struct {
	Config                NewsContextConfig      `json:"config"`
	ThemeCount            int                    `json:"themeCount"`
	ActiveThemeCount      int                    `json:"activeThemeCount"`
	ChangedThemeCount     int                    `json:"changedThemeCount"`
	CurrentNewsCount      int                    `json:"currentNewsCount"`
	ProcessedNewsCount    int                    `json:"historicalProcessedCount"`
	PendingNewsCount      int                    `json:"pendingNewsCount"`
	CompactedNewsCount    int                    `json:"compressedNewsCount"`
	ProtectedNewsCount    int                    `json:"protectedNewsCount"`
	ReleasedBytes         int64                  `json:"releasedBytes"`
	PendingReviewCount    int                    `json:"pendingReviewCount"`
	PendingCleanupCount   int                    `json:"pendingCleanupCount"`
	ReadyIndexCount       int                    `json:"indexReadyCount"`
	MissingIndexCount     int                    `json:"indexMissingCount"`
	StaleIndexCount       int                    `json:"indexStaleCount"`
	FailedIndexCount      int                    `json:"indexFailedCount"`
	IndexStatus           string                 `json:"indexStatus,omitempty"`
	IndexError            string                 `json:"indexError,omitempty"`
	MCPEnabled            bool                   `json:"mcpAvailable"`
	MCPToolsReady         bool                   `json:"mcpToolsReady"`
	MCPLastVerifiedAt     time.Time              `json:"mcpLastVerifiedAt,omitempty"`
	MCPVerificationStatus string                 `json:"mcpVerificationStatus,omitempty"`
	MCPError              string                 `json:"mcpError,omitempty"`
	LatestRun             *NewsContextRun        `json:"latestRun,omitempty"`
	LatestCleanup         *NewsContextCleanupRun `json:"latestCleanup,omitempty"`
	UpdatedAt             time.Time              `json:"updatedAt"`
}

func validNewsContextWindowType(value string) bool {
	switch strings.TrimSpace(value) {
	case NewsContextWindowHourly, NewsContextWindowFourHour, NewsContextWindowDaily:
		return true
	default:
		return false
	}
}

func validNewsThreadStage(value string) bool {
	switch strings.TrimSpace(value) {
	case NewsThreadStageEmerging, NewsThreadStageSpreading, NewsThreadStageAccelerating,
		NewsThreadStageOverheated, NewsThreadStageDiverging, NewsThreadStageRetreating,
		NewsThreadStageDormant, NewsThreadStageRestarting:
		return true
	default:
		return false
	}
}

func newsContextWorseResearchStatus(current, candidate string) string {
	normalize := func(value string) string {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "", NewsContextResearchNotRequired:
			return NewsContextResearchNotRequired
		case NewsContextResearchCompleted, "verified":
			return NewsContextResearchCompleted
		case NewsContextResearchUnresolved:
			return NewsContextResearchUnresolved
		case NewsContextResearchUnavailable:
			return NewsContextResearchUnavailable
		case NewsContextResearchFailed:
			return NewsContextResearchFailed
		default:
			return NewsContextResearchUnresolved
		}
	}
	severity := func(value string) int {
		switch value {
		case NewsContextResearchFailed:
			return 4
		case NewsContextResearchUnavailable:
			return 3
		case NewsContextResearchUnresolved:
			return 2
		case NewsContextResearchCompleted:
			return 1
		default:
			return 0
		}
	}
	current = normalize(current)
	candidate = normalize(candidate)
	if severity(candidate) > severity(current) {
		return candidate
	}
	return current
}
