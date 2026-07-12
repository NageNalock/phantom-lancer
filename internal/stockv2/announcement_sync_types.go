package stockv2

import "time"

const (
	announcementSyncDefaultPageSize        = 30
	announcementSyncMaxPageSize            = 30
	announcementSyncDefaultMaxPages        = 200
	announcementSyncDefaultOverlap         = 6 * time.Hour
	announcementSyncDefaultInitialLookback = 48 * time.Hour
	announcementLateRecheckLookbackDays    = 30
	announcementSyncClockSkew              = 5 * time.Minute
)

// AnnouncementMarketPage is one exchange-wide page returned by CNINFO.
type AnnouncementMarketPage struct {
	Market        string
	Page          int
	PageSize      int
	Total         int
	RawCount      int
	HasMore       bool
	Announcements []StockV2Announcement
}

// AnnouncementSyncState is the durable coverage cursor for one source and market.
// CoveredThrough advances only after every requested page has been stored;
// LateRecheckCoveredThrough advances by at most one Shanghai date bucket per day.
type AnnouncementSyncState struct {
	Source                    string
	Market                    string
	CoveredThrough            time.Time
	LatestPublishedAt         time.Time
	LastSuccessAt             time.Time
	LastWindowStart           time.Time
	LastWindowEnd             time.Time
	LastPageCount             int
	LastFetchedCount          int
	LastInsertedCount         int
	LateRecheckStartedAt      time.Time
	LateRecheckCoveredThrough time.Time
	LastLateRecheckAt         time.Time
	CreatedAt                 time.Time
	UpdatedAt                 time.Time
}

type AnnouncementMarketsSyncRequest struct {
	Markets         []string
	PageSize        int
	MaxPages        int
	Overlap         time.Duration
	InitialLookback time.Duration
	Now             time.Time
}

type AnnouncementMarketSyncResult struct {
	Market                  string
	WindowStart             time.Time
	WindowEnd               time.Time
	PagesFetched            int
	FetchedCount            int
	InsertedCount           int
	LatestPublishedAt       time.Time
	LateRecheckDate         time.Time
	LateRecheckPagesFetched int
	LateRecheckFetchedCount int
}

type AnnouncementMarketsSyncResult struct {
	Markets     []AnnouncementMarketSyncResult
	NewBySymbol map[string][]StockV2Announcement
	StartedAt   time.Time
	FinishedAt  time.Time
}
