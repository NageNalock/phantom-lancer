package stockv2

import "time"

const (
	AssetMaintenanceScopeFullUniverse   = "full_universe"
	AssetMaintenanceScopeExplicit       = "explicit_symbols"
	AssetMaintenanceScopeCappedRotation = "capped_rotation"
	AssetMaintenanceScopeLegacyUnknown  = "legacy_unknown"

	AssetMaintenanceCoveragePending    = "pending"
	AssetMaintenanceCoverageCovered    = "covered"
	AssetMaintenanceCoverageIncomplete = "incomplete"

	AssetMaintenanceFreshnessPending  = "pending"
	AssetMaintenanceFreshnessReady    = "ready"
	AssetMaintenanceFreshnessStale    = "stale"
	AssetMaintenanceFreshnessRetrying = "retrying"
	AssetMaintenanceFreshnessFailed   = "failed"

	AssetMaintenanceItemStatusPending   = "pending"
	AssetMaintenanceItemStatusRunning   = "running"
	AssetMaintenanceItemStatusCompleted = "completed"
	AssetMaintenanceItemStatusRetryWait = "retry_wait"
	AssetMaintenanceItemStatusFailed    = "failed"

	AssetDailyBarStatusSkipped    = "skipped"
	AssetDailyBarStatusFetched    = "fetched"
	AssetDailyBarStatusIncomplete = "incomplete"
	AssetDailyBarStatusFailed     = "failed"

	AssetDailyFlowStatusReady       = "ready"
	AssetDailyFlowStatusIncomplete  = "incomplete"
	AssetDailyFlowStatusNotRequired = "not_required"

	AssetBaseProfileStatusSkipped   = "skipped"
	AssetBaseProfileStatusUpdated   = "updated"
	AssetBaseProfileStatusUnchanged = "unchanged"
	AssetBaseProfileStatusFailed    = "failed"

	AssetAnnouncementStatusSkipped = "skipped"
	AssetAnnouncementStatusChecked = "checked"
	AssetAnnouncementStatusFailed  = "failed"

	AssetAIDecisionMissing         = "called_missing"
	AssetAIDecisionBaseChanged     = "called_base_changed"
	AssetAIDecisionAnnouncement    = "called_announcement"
	AssetAIDecisionRetry           = "called_retry"
	AssetAIDecisionManualForce     = "called_manual_force"
	AssetAIDecisionSkippedUnneeded = "skipped_unneeded"
	AssetAIDecisionSkippedConfig   = "skipped_not_configured"
	AssetAIDecisionFailed          = "failed"

	StockV2AnnouncementSourceCninfo    = "cninfo"
	AnnouncementBodyStatusMetadataOnly = "metadata_only"
	AnnouncementBodyStatusProcessing   = "processing"
	AnnouncementBodyStatusRetryWait    = "retry_wait"
	AnnouncementBodyStatusTextReady    = "text_ready"
	AnnouncementBodyStatusFailed       = "failed_terminal"

	AssetAIProgressStatusNotRequired           = "not_required"
	AssetAIProgressStatusActive                = "active"
	AssetAIProgressStatusCompleted             = "completed"
	AssetAIProgressStatusCompletedWithFailures = "completed_with_failures"
)

type AssetMaintenanceStats struct {
	DailyBarFetched       int `json:"dailyBarFetched"`
	DailyBarSkipped       int `json:"dailyBarSkipped"`
	BaseProfileUpdated    int `json:"baseProfileUpdated"`
	BaseProfileUnchanged  int `json:"baseProfileUnchanged"`
	AnnouncementsNew      int `json:"announcementsNew"`
	MajorAnnouncementsNew int `json:"majorAnnouncementsNew"`
	AICalled              int `json:"aiCalled"`
	AISkipped             int `json:"aiSkipped"`
	AIQueued              int `json:"aiQueued"`
	AIRunning             int `json:"aiRunning"`
	AICompleted           int `json:"aiCompleted"`
	AIFailed              int `json:"aiFailed"`
}

// AssetMaintenanceJobProgress keeps the deterministic data pipeline separate
// from the asynchronous AI profile queue. The parent update job status only
// describes Base; AIProfile may remain active after Base has completed.
type AssetMaintenanceJobProgress struct {
	Coverage  AssetMaintenanceCoverageProgress `json:"coverage"`
	Assets    AssetMaintenanceAssetsProgress   `json:"assets"`
	AIProfile AssetMaintenanceAIProgress       `json:"aiProfile"`
}

type AssetMaintenanceCoverageProgress struct {
	Status       string `json:"status"`
	Target       int    `json:"target"`
	Checked      int    `json:"checked"`
	Pending      int    `json:"pending"`
	Retrying     int    `json:"retrying"`
	Failed       int    `json:"failed"`
	UniverseHash string `json:"universeHash,omitempty"`
	CutoffDate   string `json:"cutoffDate,omitempty"`
}

type AssetMaintenanceAssetsProgress struct {
	Status       string `json:"status"`
	MarketFresh  int    `json:"marketFresh"`
	MessageFresh int    `json:"messageFresh"`
	Fresh        int    `json:"fresh"`
	Stale        int    `json:"stale"`
	Retrying     int    `json:"retrying"`
	Failed       int    `json:"failed"`
}

type AssetMaintenanceAIProgress struct {
	Status      string `json:"status"`
	Requested   int    `json:"requested"`
	Pending     int    `json:"pending"`
	Queued      int    `json:"queued"`
	Running     int    `json:"running"`
	Retrying    int    `json:"retrying"`
	Completed   int    `json:"completed"`
	Failed      int    `json:"failed"`
	Skipped     int    `json:"skipped"`
	Outstanding int    `json:"outstanding"`
}

type AssetMaintenanceSourceStatus struct {
	Source    string    `json:"source"`
	Status    string    `json:"status"`
	Message   string    `json:"message,omitempty"`
	CheckedAt time.Time `json:"checkedAt,omitempty"`
}

type AssetMaintenanceItem struct {
	ID                    string                         `json:"id"`
	JobID                 string                         `json:"jobId,omitempty"`
	Symbol                string                         `json:"symbol"`
	Market                string                         `json:"market,omitempty"`
	InstrumentType        string                         `json:"instrumentType,omitempty"`
	Name                  string                         `json:"name,omitempty"`
	Status                string                         `json:"status"`
	PriorityReason        string                         `json:"priorityReason,omitempty"`
	AttemptCount          int                            `json:"attemptCount"`
	NextRetryAt           time.Time                      `json:"nextRetryAt,omitempty"`
	CheckedAt             time.Time                      `json:"checkedAt,omitempty"`
	ExpectedLatestDate    string                         `json:"expectedLatestTradeDate,omitempty"`
	DailyBarStatus        string                         `json:"dailyBarStatus,omitempty"`
	DailyBarGapCount      int                            `json:"dailyBarGapCount"`
	DailyBarMissingFacets int                            `json:"dailyBarMissingFacetCount"`
	DailyFlowStatus       string                         `json:"dailyFlowStatus,omitempty"`
	DailyBarFetched       int                            `json:"dailyBarFetched"`
	DailyBarStart         string                         `json:"dailyBarStart,omitempty"`
	DailyBarEnd           string                         `json:"dailyBarEnd,omitempty"`
	BaseProfileStatus     string                         `json:"baseProfileStatus,omitempty"`
	BaseProfileChanged    bool                           `json:"baseProfileChanged"`
	BaseProfileHashBefore string                         `json:"baseProfileHashBefore,omitempty"`
	BaseProfileHashAfter  string                         `json:"baseProfileHashAfter,omitempty"`
	AnnouncementStatus    string                         `json:"announcementStatus,omitempty"`
	AnnouncementsNew      int                            `json:"announcementsNew"`
	MajorAnnouncementsNew int                            `json:"majorAnnouncementsNew"`
	AIDecision            string                         `json:"aiDecision,omitempty"`
	AIProfileStatus       string                         `json:"aiProfileStatus,omitempty"`
	AIQueueStatus         string                         `json:"aiQueueStatus,omitempty"`
	AIDesiredInputVersion string                         `json:"aiDesiredInputVersion,omitempty"`
	AgentRunID            string                         `json:"agentRunId,omitempty"`
	ErrorMessage          string                         `json:"errorMessage,omitempty"`
	SourceStatuses        []AssetMaintenanceSourceStatus `json:"sourceStatuses,omitempty"`
	DurationMs            int64                          `json:"durationMs"`
	StartedAt             time.Time                      `json:"startedAt"`
	FinishedAt            time.Time                      `json:"finishedAt,omitempty"`
	CreatedAt             time.Time                      `json:"createdAt"`
	UpdatedAt             time.Time                      `json:"updatedAt"`
}

type AssetMaintenanceSlot struct {
	SlotStart               time.Time `json:"slotStart"`
	SlotEnd                 time.Time `json:"slotEnd"`
	ExpectedLatestTradeDate string    `json:"expectedLatestTradeDate,omitempty"`
	UniverseSnapshotID      string    `json:"universeSnapshotId"`
	UniverseHash            string    `json:"universeHash"`
	TargetCount             int       `json:"targetCount"`
	JobID                   string    `json:"jobId,omitempty"`
	Status                  string    `json:"status"`
	CoveredAt               time.Time `json:"coveredAt,omitempty"`
	CreatedAt               time.Time `json:"createdAt"`
	UpdatedAt               time.Time `json:"updatedAt"`
}

type AssetUniverseSnapshot struct {
	ID           string    `json:"id"`
	UniverseHash string    `json:"universeHash"`
	Status       string    `json:"status"`
	Source       string    `json:"source"`
	TargetCount  int       `json:"targetCount"`
	CreatedAt    time.Time `json:"createdAt"`
}

type AssetMaintenanceItemListFilter struct {
	JobID  string
	Symbol string
	Limit  int
	Offset int
}

type StockV2Announcement struct {
	ID                string    `json:"id"`
	Source            string    `json:"source"`
	Symbol            string    `json:"symbol"`
	Market            string    `json:"market,omitempty"`
	OrgID             string    `json:"orgId,omitempty"`
	Title             string    `json:"title"`
	Category          string    `json:"category,omitempty"`
	AnnouncementID    string    `json:"announcementId,omitempty"`
	PDFURL            string    `json:"pdfUrl,omitempty"`
	ContentHash       string    `json:"contentHash"`
	DedupeKey         string    `json:"dedupeKey,omitempty"`
	SymbolRevision    int64     `json:"symbolRevision,omitempty"`
	Major             bool      `json:"major"`
	MajorReason       string    `json:"majorReason,omitempty"`
	PublishedAt       time.Time `json:"publishedAt,omitempty"`
	FetchedAt         time.Time `json:"fetchedAt"`
	FirstFetchedAt    time.Time `json:"firstFetchedAt,omitempty"`
	LastSeenAt        time.Time `json:"lastSeenAt,omitempty"`
	BodyStatus        string    `json:"bodyStatus,omitempty"`
	BodyTextExcerpt   string    `json:"bodyTextExcerpt,omitempty"`
	BodyHash          string    `json:"bodyHash,omitempty"`
	BodyCheckedAt     time.Time `json:"bodyCheckedAt,omitempty"`
	BodyError         string    `json:"bodyError,omitempty"`
	BodyAttemptCount  int       `json:"bodyAttemptCount"`
	BodyNextAttemptAt time.Time `json:"bodyNextAttemptAt,omitempty"`
	BodyContentBytes  int64     `json:"bodyContentBytes"`
	CreatedAt         time.Time `json:"createdAt"`
	UpdatedAt         time.Time `json:"updatedAt"`
}

type AnnouncementListFilter struct {
	Symbol    string
	MajorOnly bool
	Limit     int
	Offset    int
}

type StockV2AssetSummary struct {
	Symbol                                   string                `json:"symbol"`
	DailyBarQuality                          DailyBarsQuality      `json:"dailyBarQuality"`
	ProfileSummary                           StockProfileSummary   `json:"profileSummary"`
	LatestAnnouncementAt                     time.Time             `json:"latestAnnouncementAt,omitempty"`
	LatestAnnouncementFetchedAt              time.Time             `json:"latestAnnouncementFetchedAt,omitempty"`
	LatestAnnouncementTitle                  string                `json:"latestAnnouncementTitle,omitempty"`
	AnnouncementCount                        int                   `json:"announcementCount"`
	MajorAnnouncementCount                   int                   `json:"majorAnnouncementCount"`
	MajorAnnouncementContentUnavailableCount int                   `json:"majorAnnouncementContentUnavailableCount"`
	LatestMaintenance                        AssetMaintenanceItem  `json:"latestMaintenance,omitempty"`
	Readiness                                StockV2AssetReadiness `json:"readiness"`
}

// StockV2AssetReadiness is the legacy shape embedded by the asset-summary API.
// DEPRECATED: remove after 2026-08-31, once the bundled web client has used the
// unified market/message/analysis readiness API for two releases.
type StockV2AssetReadiness struct {
	Ready              bool      `json:"ready"`
	DataReady          bool      `json:"dataReady"`
	DailyBarReady      bool      `json:"dailyBarReady"`
	BaseProfileReady   bool      `json:"baseProfileReady"`
	AnnouncementReady  bool      `json:"announcementReady"`
	AIProfileReady     bool      `json:"aiProfileReady"`
	Reasons            []string  `json:"reasons,omitempty"`
	AnnouncementSyncAt time.Time `json:"announcementSyncAt,omitempty"`
	EvaluatedAt        time.Time `json:"evaluatedAt"`
}

type AssetMaintainSymbolRequest struct {
	ForceAI       bool   `json:"forceAi,omitempty"`
	TriggerSource string `json:"triggerSource,omitempty"`
	RequestedBy   string `json:"requestedBy,omitempty"`
}

type AssetMaintainSymbolResult struct {
	Item          AssetMaintenanceItem  `json:"item"`
	Profile       StockProfile          `json:"profile,omitempty"`
	Announcements []StockV2Announcement `json:"announcements,omitempty"`
	AgentRun      *AgentRun             `json:"agentRun,omitempty"`
}

type StockProfilePreviousSummary struct {
	BusinessSummaryZh string    `json:"businessSummaryZh,omitempty"`
	BusinessSummaryEn string    `json:"businessSummaryEn,omitempty"`
	ProfileTextZh     string    `json:"profileTextZh,omitempty"`
	ProfileTextEn     string    `json:"profileTextEn,omitempty"`
	AIProfileModel    string    `json:"aiProfileModel,omitempty"`
	UpdatedAt         time.Time `json:"updatedAt,omitempty"`
}

type StockProfileBaseDiff struct {
	HashBefore string   `json:"hashBefore,omitempty"`
	HashAfter  string   `json:"hashAfter,omitempty"`
	Changed    bool     `json:"changed"`
	Fields     []string `json:"fields,omitempty"`
}

type StockProfileDailySummary struct {
	LatestDate    string  `json:"latestDate,omitempty"`
	RowCount      int     `json:"rowCount"`
	PctChange5D   float64 `json:"pctChange5d,omitempty"`
	MainNetInflow float64 `json:"mainNetInflow,omitempty"`
}

type StockProfileSummaryContext struct {
	Profile              StockProfile                   `json:"profile"`
	PreviousSummary      StockProfilePreviousSummary    `json:"previousSummary,omitempty"`
	BaseDiff             StockProfileBaseDiff           `json:"baseDiff"`
	NewAnnouncements     []StockV2Announcement          `json:"newAnnouncements,omitempty"`
	MajorAnnouncements   []StockV2Announcement          `json:"majorAnnouncements,omitempty"`
	DailySummary         StockProfileDailySummary       `json:"dailySummary,omitempty"`
	SourceStatuses       []AssetMaintenanceSourceStatus `json:"sourceStatuses,omitempty"`
	MaintenanceStartedAt time.Time                      `json:"maintenanceStartedAt"`
}
