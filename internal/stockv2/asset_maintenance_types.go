package stockv2

import "time"

const (
	AssetMaintenanceItemStatusRunning   = "running"
	AssetMaintenanceItemStatusCompleted = "completed"
	AssetMaintenanceItemStatusPartial   = "partial"
	AssetMaintenanceItemStatusFailed    = "failed"

	AssetDailyBarStatusSkipped = "skipped"
	AssetDailyBarStatusFetched = "fetched"
	AssetDailyBarStatusFailed  = "failed"

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
	AssetAIDecisionSkippedBudget   = "skipped_budget_exhausted"
	AssetAIDecisionSkippedConfig   = "skipped_not_configured"
	AssetAIDecisionFailed          = "failed"

	StockV2AnnouncementSourceCninfo = "cninfo"
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
	DailyBarStatus        string                         `json:"dailyBarStatus,omitempty"`
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
	AgentRunID            string                         `json:"agentRunId,omitempty"`
	ErrorMessage          string                         `json:"errorMessage,omitempty"`
	SourceStatuses        []AssetMaintenanceSourceStatus `json:"sourceStatuses,omitempty"`
	DurationMs            int64                          `json:"durationMs"`
	StartedAt             time.Time                      `json:"startedAt"`
	FinishedAt            time.Time                      `json:"finishedAt,omitempty"`
	CreatedAt             time.Time                      `json:"createdAt"`
	UpdatedAt             time.Time                      `json:"updatedAt"`
}

type AssetMaintenanceItemListFilter struct {
	JobID  string
	Symbol string
	Limit  int
	Offset int
}

type StockV2Announcement struct {
	ID             string    `json:"id"`
	Source         string    `json:"source"`
	Symbol         string    `json:"symbol"`
	Market         string    `json:"market,omitempty"`
	OrgID          string    `json:"orgId,omitempty"`
	Title          string    `json:"title"`
	Category       string    `json:"category,omitempty"`
	AnnouncementID string    `json:"announcementId,omitempty"`
	PDFURL         string    `json:"pdfUrl,omitempty"`
	ContentHash    string    `json:"contentHash"`
	Major          bool      `json:"major"`
	MajorReason    string    `json:"majorReason,omitempty"`
	PublishedAt    time.Time `json:"publishedAt,omitempty"`
	FetchedAt      time.Time `json:"fetchedAt"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

type AnnouncementListFilter struct {
	Symbol    string
	MajorOnly bool
	Limit     int
	Offset    int
}

type StockV2AssetSummary struct {
	Symbol                  string               `json:"symbol"`
	DailyBarQuality         DailyBarsQuality     `json:"dailyBarQuality"`
	ProfileSummary          StockProfileSummary  `json:"profileSummary"`
	LatestAnnouncementAt    time.Time            `json:"latestAnnouncementAt,omitempty"`
	LatestAnnouncementTitle string               `json:"latestAnnouncementTitle,omitempty"`
	AnnouncementCount       int                  `json:"announcementCount"`
	MajorAnnouncementCount  int                  `json:"majorAnnouncementCount"`
	LatestMaintenance       AssetMaintenanceItem `json:"latestMaintenance,omitempty"`
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
