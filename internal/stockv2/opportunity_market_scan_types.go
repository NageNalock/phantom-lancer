package stockv2

import (
	"errors"
	"time"
)

const (
	OpportunityMarketScanConfigID       = "default"
	OpportunityDiscoveryModeManualTheme = "manual_theme"
	OpportunityDiscoveryModeMarketScan  = "market_scan"
	OpportunityMarketScanCreatedBy      = "system:market_scan"

	OpportunityMarketScanStatusPending      = "pending"
	OpportunityMarketScanStatusPrefiltering = "prefiltering"
	OpportunityMarketScanStatusEnriching    = "enriching"
	OpportunityMarketScanStatusResearching  = "researching"
	OpportunityMarketScanStatusDrafting     = "drafting"
	OpportunityMarketScanStatusCompleted    = "completed"
	OpportunityMarketScanStatusPartial      = "partial"
	OpportunityMarketScanStatusFailed       = "failed"

	OpportunityMarketScanTriggerManual       = "manual"
	OpportunityMarketScanTriggerScheduled    = "scheduled"
	OpportunityMarketScanTriggerThemeRefresh = "theme_refresh"

	OpportunityMarketScanSourcePrice   = "price"
	OpportunityMarketScanSourceSector  = "sector"
	OpportunityMarketScanSourceMessage = "message"
	OpportunityMarketScanSourceMixed   = "mixed"

	OpportunityMarketSectorStateEmerging    = "emerging"
	OpportunityMarketSectorStateConfirmed   = "confirmed"
	OpportunityMarketSectorStateOverheated  = "overheated"
	OpportunityMarketSectorStateFading      = "fading"
	OpportunityMarketSectorStateInvalidated = "invalidated"

	OpportunityMarketThemeMatchDirect     = "direct_symbol"
	OpportunityMarketThemeMatchStructured = "structured_term"
	OpportunityMarketThemeMatchKeyword    = "profile_keyword"
	OpportunityMarketThemeMatchSemantic   = "semantic_recall"

	OpportunityMarketScanCandidatePrefiltered = "prefiltered"
	OpportunityMarketScanCandidateResearch    = "research_candidate"
	OpportunityMarketScanCandidateReviewedOut = "reviewed_out"
	OpportunityMarketScanCandidateFinal       = "final"
	OpportunityMarketScanCandidateExcluded    = "excluded"

	OpportunityMarketScanStrategyPending   = "pending"
	OpportunityMarketScanStrategyGenerated = "generated"
	OpportunityMarketScanStrategySkipped   = "skipped"
)

const (
	// ponytail: these fixed budgets make one personal-server scan predictable
	// without adding a tuning surface. Every final candidate is now eligible for
	// strategy drafting; promote the limits to feature settings only if the owner
	// later needs to trade breadth for cost.
	opportunityMarketScanLocalLimit      = 200
	opportunityMarketScanQFQLimit        = 60
	opportunityMarketScanFundFlowLimit   = 30
	opportunityMarketScanResearchLimit   = 20
	opportunityMarketScanFinalLimit      = 10
	opportunityMarketScanStrategyLimit   = opportunityMarketScanFinalLimit
	opportunityMarketScanMinimumCoverage = 0.80
	// ponytail: reserve existing bounded slots for fresh material message themes;
	// this changes candidate composition without increasing quote, provider, or model budgets.
	opportunityMarketScanPriceLocalReserve         = 160
	opportunityMarketScanSectorLocalReserve        = 24
	opportunityMarketScanSectorResearchReserve     = 8
	opportunityMarketScanSectorLimit               = 6
	opportunityMarketScanSectorRepresentativeLimit = 4
	opportunityMarketScanMessageLocalReserve       = 16
	opportunityMarketScanMessageResearchReserve    = 6
	opportunityMarketScanThemeVersionLimit         = 6
	opportunityMarketScanThemeSemanticLimit        = 12
	// ponytail: sector rotation becomes misleading when too much of the eligible
	// universe has no industry label. Keep the hard quality boundary fixed until
	// the owner has a reason to trade correctness for scan availability.
	opportunityMarketScanMinimumSectorCoverage = 0.95
)

var (
	ErrOpportunityMarketScanConfigNotFound = errors.New("opportunity market scan config not found")
	ErrOpportunityMarketScanRunNotFound    = errors.New("opportunity market scan run not found")
	ErrOpportunityMarketScanAlreadyRunning = errors.New("opportunity market scan already running")
	ErrOpportunityMarketScanDataNotReady   = errors.New("opportunity market scan data coverage is not ready")
	ErrOpportunityMarketScanInvalidState   = errors.New("invalid opportunity market scan state")
)

type OpportunityMarketScanConfig struct {
	ID                            string    `json:"id"`
	Enabled                       bool      `json:"enabled"`
	LastScannedTradeDate          string    `json:"lastScannedTradeDate,omitempty"`
	LastRunID                     string    `json:"lastRunId,omitempty"`
	LastRunStatus                 string    `json:"lastRunStatus,omitempty"`
	LastRunAt                     time.Time `json:"lastRunAt,omitempty"`
	LastSuccessAt                 time.Time `json:"lastSuccessAt,omitempty"`
	LastError                     string    `json:"lastError,omitempty"`
	UpdatedAt                     time.Time `json:"updatedAt"`
	PrimaryFundFlowAPIKey         string    `json:"-"`
	BackupFundFlowAPIKey          string    `json:"-"`
	BackupFundFlowProxy           string    `json:"-"`
	PrimaryFundFlowConfigured     bool      `json:"primaryFundFlowConfigured"`
	BackupFundFlowConfigured      bool      `json:"backupFundFlowConfigured"`
	BackupFundFlowProxyConfigured bool      `json:"backupFundFlowProxyConfigured"`
}

type RequestUpdateOpportunityMarketScanConfig struct {
	Enabled                    *bool  `json:"enabled,omitempty"`
	PrimaryFundFlowAPIKey      string `json:"primaryFundFlowApiKey,omitempty"`
	BackupFundFlowAPIKey       string `json:"backupFundFlowApiKey,omitempty"`
	BackupFundFlowProxy        string `json:"backupFundFlowProxy,omitempty"`
	ClearPrimaryFundFlowAPIKey bool   `json:"clearPrimaryFundFlowApiKey,omitempty"`
	ClearBackupFundFlowAPIKey  bool   `json:"clearBackupFundFlowApiKey,omitempty"`
	ClearBackupFundFlowProxy   bool   `json:"clearBackupFundFlowProxy,omitempty"`
}

type OpportunityMarketScanRun struct {
	ID                     string                          `json:"id"`
	TriggerType            string                          `json:"triggerType"`
	RequestedBy            string                          `json:"requestedBy,omitempty"`
	Status                 string                          `json:"status"`
	TradeDate              string                          `json:"tradeDate,omitempty"`
	SourceUpdateJobID      string                          `json:"sourceUpdateJobId,omitempty"`
	OpportunityID          string                          `json:"opportunityId,omitempty"`
	DiscoveryRunID         string                          `json:"discoveryRunId,omitempty"`
	StrategyAgentRunID     string                          `json:"strategyAgentRunId,omitempty"`
	UniverseCount          int                             `json:"universeCount"`
	CoveredCount           int                             `json:"coveredCount"`
	PrefilterCount         int                             `json:"prefilterCount"`
	EnrichedCount          int                             `json:"enrichedCount"`
	ResearchCount          int                             `json:"researchCount"`
	FinalCandidateCount    int                             `json:"finalCandidateCount"`
	StrategyRequestedCount int                             `json:"strategyRequestedCount"`
	StrategyCreatedCount   int                             `json:"strategyCreatedCount"`
	FundFlowRequestedCount int                             `json:"fundFlowRequestedCount"`
	FundFlowAvailableCount int                             `json:"fundFlowAvailableCount"`
	FundFlowSource         string                          `json:"fundFlowSource,omitempty"`
	FundFlowUsed           bool                            `json:"fundFlowUsed"`
	FundFlowStatus         string                          `json:"fundFlowStatus,omitempty"`
	FundFlowError          string                          `json:"fundFlowError,omitempty"`
	ThemeSnapshot          OpportunityMarketThemeSnapshot  `json:"themeSnapshot"`
	SectorSnapshot         OpportunityMarketSectorSnapshot `json:"sectorSnapshot"`
	RetryCount             int                             `json:"retryCount"`
	NextRetryAt            time.Time                       `json:"nextRetryAt,omitempty"`
	ErrorMessage           string                          `json:"errorMessage,omitempty"`
	StartedAt              time.Time                       `json:"startedAt,omitempty"`
	FinishedAt             time.Time                       `json:"finishedAt,omitempty"`
	CreatedAt              time.Time                       `json:"createdAt"`
	UpdatedAt              time.Time                       `json:"updatedAt"`
}

type OpportunityMarketSectorSnapshot struct {
	CapturedAt         time.Time                      `json:"capturedAt,omitempty"`
	TradeDate          string                         `json:"tradeDate,omitempty"`
	Status             string                         `json:"status,omitempty"`
	EligibleCount      int                            `json:"eligibleCount"`
	ClassifiedCount    int                            `json:"classifiedCount"`
	CoverageRatio      float64                        `json:"coverageRatio"`
	TrackedSectorCount int                            `json:"trackedSectorCount"`
	Trends             []OpportunityMarketSectorTrend `json:"trends,omitempty"`
	Message            string                         `json:"message,omitempty"`
}

type OpportunityMarketSectorTrend struct {
	Key                   string   `json:"key"`
	Name                  string   `json:"name"`
	State                 string   `json:"state"`
	PreviousState         string   `json:"previousState,omitempty"`
	Score                 float64  `json:"score"`
	MemberCount           int      `json:"memberCount"`
	AboveMA20Ratio        float64  `json:"aboveMa20Ratio"`
	AboveMA20Delta3       float64  `json:"aboveMa20Delta3"`
	Positive5DayRatio     float64  `json:"positive5DayRatio"`
	MedianReturn3Pct      float64  `json:"medianReturn3Pct"`
	MedianReturn5Pct      float64  `json:"medianReturn5Pct"`
	MedianReturn20Pct     float64  `json:"medianReturn20Pct"`
	VolumeExpansionRatio  float64  `json:"volumeExpansionRatio"`
	Top200Count           int      `json:"top200Count"`
	FirstSeenTradeDate    string   `json:"firstSeenTradeDate,omitempty"`
	Streak                int      `json:"streak"`
	RepresentativeSymbols []string `json:"representativeSymbols,omitempty"`
	RepresentativeNames   []string `json:"representativeNames,omitempty"`
}

type OpportunityMarketSectorSignal struct {
	Key                string  `json:"key"`
	Name               string  `json:"name"`
	State              string  `json:"state"`
	Score              float64 `json:"score"`
	FirstSeenTradeDate string  `json:"firstSeenTradeDate,omitempty"`
	Streak             int     `json:"streak"`
}

type OpportunityMarketThemeSnapshot struct {
	CapturedAt            time.Time `json:"capturedAt,omitempty"`
	Since                 time.Time `json:"since,omitempty"`
	Status                string    `json:"status,omitempty"`
	VersionIDs            []string  `json:"versionIds,omitempty"`
	VersionCount          int       `json:"versionCount"`
	MatchedCandidateCount int       `json:"matchedCandidateCount"`
	MessageCandidateCount int       `json:"messageCandidateCount"`
	SemanticAvailable     bool      `json:"semanticAvailable"`
	SemanticQueryCount    int       `json:"semanticQueryCount"`
	SemanticFailureCount  int       `json:"semanticFailureCount"`
	Message               string    `json:"message,omitempty"`
}

type OpportunityMarketThemeMatch struct {
	ThreadID                   string    `json:"threadId"`
	VersionID                  string    `json:"versionId"`
	Title                      string    `json:"title"`
	Stage                      string    `json:"stage,omitempty"`
	Confidence                 float64   `json:"confidence,omitempty"`
	MaterialChange             bool      `json:"materialChange"`
	EffectiveAt                time.Time `json:"effectiveAt,omitempty"`
	MatchKind                  string    `json:"matchKind"`
	MatchedTerms               []string  `json:"matchedTerms,omitempty"`
	SemanticScore              float64   `json:"semanticScore,omitempty"`
	RequiresCausalVerification bool      `json:"requiresCausalVerification"`
}

type OpportunityMarketScanMetrics struct {
	InstrumentType     string                          `json:"instrumentType,omitempty"`
	TradeDate          string                          `json:"tradeDate,omitempty"`
	Return5Pct         float64                         `json:"return5Pct,omitempty"`
	Return20Pct        float64                         `json:"return20Pct,omitempty"`
	Return60Pct        float64                         `json:"return60Pct,omitempty"`
	MA20GapPct         float64                         `json:"ma20GapPct,omitempty"`
	MA60GapPct         float64                         `json:"ma60GapPct,omitempty"`
	VolumeRatio5To20   float64                         `json:"volumeRatio5To20,omitempty"`
	UpVolumeShare20    float64                         `json:"upVolumeShare20,omitempty"`
	Volatility20       float64                         `json:"volatility20,omitempty"`
	ATR14              float64                         `json:"atr14,omitempty"`
	ATR14Pct           float64                         `json:"atr14Pct,omitempty"`
	MA20               float64                         `json:"ma20,omitempty"`
	MedianAmount20     float64                         `json:"medianAmount20,omitempty"`
	IndustryBreadth20  float64                         `json:"industryBreadth20,omitempty"`
	MainNetInflow5     float64                         `json:"mainNetInflow5,omitempty"`
	MainNetInflow20    float64                         `json:"mainNetInflow20,omitempty"`
	MainNetInflow60    float64                         `json:"mainNetInflow60,omitempty"`
	MainFlowRatio20    float64                         `json:"mainFlowRatio20,omitempty"`
	PositiveFlowDays20 int                             `json:"positiveFlowDays20,omitempty"`
	LatestPrice        float64                         `json:"latestPrice,omitempty"`
	LatestPrevClose    float64                         `json:"latestPrevClose,omitempty"`
	LatestOpenPrice    float64                         `json:"latestOpenPrice,omitempty"`
	LatestHighPrice    float64                         `json:"latestHighPrice,omitempty"`
	LatestLowPrice     float64                         `json:"latestLowPrice,omitempty"`
	LatestAmount       float64                         `json:"latestAmount,omitempty"`
	LatestPctChange    float64                         `json:"latestPctChange,omitempty"`
	LatestTurnoverRate float64                         `json:"latestTurnoverRate,omitempty"`
	LatestVolumeRatio  float64                         `json:"latestVolumeRatio,omitempty"`
	LatestMainFlowPct  float64                         `json:"latestMainFlowPct,omitempty"`
	LatestQuoteAt      time.Time                       `json:"latestQuoteAt,omitempty"`
	LatestQuoteSource  string                          `json:"latestQuoteSource,omitempty"`
	QFQAvailable       bool                            `json:"qfqAvailable"`
	FundFlowAvailable  bool                            `json:"fundFlowAvailable"`
	FundFlowStatus     string                          `json:"fundFlowStatus,omitempty"`
	FundFlowSource     string                          `json:"fundFlowSource,omitempty"`
	FundFlowAsOf       string                          `json:"fundFlowAsOf,omitempty"`
	FundFlowUsed       bool                            `json:"fundFlowUsed"`
	QuoteAvailable     bool                            `json:"quoteAvailable"`
	ThemeSignals       []string                        `json:"themeSignals,omitempty"`
	CatalystSignals    []string                        `json:"catalystSignals,omitempty"`
	SourceLane         string                          `json:"sourceLane,omitempty"`
	AdmissionReasons   []string                        `json:"admissionReasons,omitempty"`
	SectorSignals      []OpportunityMarketSectorSignal `json:"sectorSignals,omitempty"`
	ThemeMatches       []OpportunityMarketThemeMatch   `json:"themeMatches,omitempty"`
	DecisionStatus     string                          `json:"decisionStatus,omitempty"`
	MarketRegime       string                          `json:"marketRegime,omitempty"`
	FactorCluster      string                          `json:"factorCluster,omitempty"`
	GateSnapshotID     string                          `json:"gateSnapshotId,omitempty"`
	DecisionGates      []DecisionGateResult            `json:"decisionGates,omitempty"`
	DataHealth         []DecisionDataHealth            `json:"dataHealth,omitempty"`
	DecisionOutcomes   []DecisionGateOutcome           `json:"decisionOutcomes,omitempty"`
}

type OpportunityMarketFundFlowProbe struct {
	OK       bool   `json:"ok"`
	Source   string `json:"source,omitempty"`
	Status   string `json:"status"`
	Count    int    `json:"count"`
	Duration int64  `json:"durationMs"`
	Error    string `json:"error,omitempty"`
}

type OpportunityDecisionDataProbe struct {
	OK        bool                                  `json:"ok"`
	Status    string                                `json:"status"`
	CheckedAt time.Time                             `json:"checkedAt"`
	Sources   map[string]OpportunityDataSourceProbe `json:"sources"`
}

type OpportunityDataSourceProbe struct {
	Status string `json:"status"`
	Source string `json:"source,omitempty"`
	Count  int    `json:"count,omitempty"`
	Error  string `json:"error,omitempty"`
}

type OpportunityMarketScanCandidate struct {
	ID                     string                       `json:"id"`
	ScanRunID              string                       `json:"scanRunId"`
	Symbol                 string                       `json:"symbol"`
	Market                 string                       `json:"market"`
	Name                   string                       `json:"name"`
	Industry               string                       `json:"industry,omitempty"`
	Sector                 string                       `json:"sector,omitempty"`
	Concepts               []string                     `json:"concepts,omitempty"`
	Stage                  string                       `json:"stage"`
	PrefilterRank          int                          `json:"prefilterRank,omitempty"`
	FinalRank              int                          `json:"finalRank,omitempty"`
	PrefilterScore         float64                      `json:"prefilterScore"`
	FinalScore             float64                      `json:"finalScore"`
	FlowScore              float64                      `json:"flowScore"`
	ThemeScore             float64                      `json:"themeScore"`
	RiskPenalty            float64                      `json:"riskPenalty"`
	Metrics                OpportunityMarketScanMetrics `json:"metrics"`
	ExclusionReason        string                       `json:"exclusionReason,omitempty"`
	DecisionReason         string                       `json:"decisionReason,omitempty"`
	HorizonOutlooks        []ModelHorizonOutlook        `json:"horizonOutlooks,omitempty"`
	OpportunityCandidateID string                       `json:"opportunityCandidateId,omitempty"`
	StrategyStatus         string                       `json:"strategyStatus,omitempty"`
	StrategyID             string                       `json:"strategyId,omitempty"`
	CreatedAt              time.Time                    `json:"createdAt"`
	UpdatedAt              time.Time                    `json:"updatedAt"`
}

type OpportunityMarketScanStatus struct {
	Config              OpportunityMarketScanConfig `json:"config"`
	ActiveRun           *OpportunityMarketScanRun   `json:"activeRun,omitempty"`
	LatestRun           *OpportunityMarketScanRun   `json:"latestRun,omitempty"`
	LatestDataTradeDate string                      `json:"latestDataTradeDate,omitempty"`
	UniverseCount       int                         `json:"universeCount"`
	CoveredCount        int                         `json:"coveredCount"`
	CoverageRatio       float64                     `json:"coverageRatio"`
	Ready               bool                        `json:"ready"`
	BlockedReason       string                      `json:"blockedReason,omitempty"`
	ScheduleDescription string                      `json:"scheduleDescription"`
	MaxRetries          int                         `json:"maxRetries"`
	Budgets             map[string]int              `json:"budgets"`
	RecommendedModel    string                      `json:"recommendedModel"`
}

type OpportunityMarketScanRunListFilter struct {
	Status string
	Limit  int
	Offset int
}

type OpportunityMarketScanCandidateListFilter struct {
	ScanRunID      string
	Stage          string
	DecisionStatus string
	SourceLane     string
	Limit          int
	Offset         int
}

func opportunityMarketScanStatusActive(status string) bool {
	switch status {
	case OpportunityMarketScanStatusPending,
		OpportunityMarketScanStatusPrefiltering,
		OpportunityMarketScanStatusEnriching,
		OpportunityMarketScanStatusResearching,
		OpportunityMarketScanStatusDrafting:
		return true
	default:
		return false
	}
}
