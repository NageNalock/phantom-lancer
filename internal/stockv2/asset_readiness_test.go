package stockv2

import (
	"testing"
	"time"
)

func TestEvaluateStockV2AssetReadinessRequiresFreshDataAndAI(t *testing.T) {
	now := time.Now()
	item := StockV2AssetSummary{
		DailyBarQuality: DailyBarsQuality{HasData: true, Meets250: true},
		ProfileSummary: StockProfileSummary{
			Market:               "SZ",
			InstrumentType:       InstrumentTypeStock,
			Status:               "ready",
			BaseProfileUpdatedAt: now.Add(-2 * time.Hour),
			AIProfileStatus:      StockProfileAIStatusReady,
			AIProfileUpdatedAt:   now.Add(-time.Hour),
		},
		LatestAnnouncementAt:        now.Add(-90 * time.Minute),
		LatestAnnouncementFetchedAt: now.Add(-90 * time.Minute),
	}
	syncState := AnnouncementSyncState{
		LastSuccessAt:  now.Add(-time.Hour),
		CoveredThrough: now.Add(-time.Hour),
	}
	got := evaluateStockV2AssetReadiness(item, syncState, now)
	if !got.Ready || !got.DataReady || !got.AIProfileReady {
		t.Fatalf("readiness = %+v, want ready", got)
	}

	item.LatestAnnouncementFetchedAt = now.Add(-30 * time.Minute)
	got = evaluateStockV2AssetReadiness(item, syncState, now)
	if got.Ready || !got.DataReady || got.AIProfileReady {
		t.Fatalf("readiness after new announcement = %+v, want data ready and ai outdated", got)
	}
}

func TestEvaluateStockV2AssetReadinessReportsStaleCoverage(t *testing.T) {
	now := time.Now()
	got := evaluateStockV2AssetReadiness(StockV2AssetSummary{
		DailyBarQuality: DailyBarsQuality{HasData: true, Meets250: true, Stale: true},
		ProfileSummary: StockProfileSummary{
			Market:               "SH",
			InstrumentType:       InstrumentTypeStock,
			Status:               "ready",
			BaseProfileUpdatedAt: now.Add(-9 * 24 * time.Hour),
			AIProfileStatus:      StockProfileAIStatusReady,
			AIProfileUpdatedAt:   now,
		},
	}, AnnouncementSyncState{
		LastSuccessAt:  now.Add(-2 * 24 * time.Hour),
		CoveredThrough: now.Add(-2 * 24 * time.Hour),
	}, now)
	if got.Ready || got.DataReady || len(got.Reasons) != 3 {
		t.Fatalf("stale readiness = %+v", got)
	}
}

func TestEvaluateStockV2AssetReadinessUsesBaseCheckForFreshnessAndBaseUpdateForAIVersion(t *testing.T) {
	now := time.Now()
	baseChangedAt := now.Add(-10 * 24 * time.Hour)
	got := evaluateStockV2AssetReadiness(StockV2AssetSummary{
		DailyBarQuality: DailyBarsQuality{HasData: true, Meets250: true},
		ProfileSummary: StockProfileSummary{
			Market: "SZ", InstrumentType: InstrumentTypeStock, Status: "ready",
			BaseProfileUpdatedAt: baseChangedAt,
			BaseProfileCheckedAt: now.Add(-time.Hour),
			AIProfileStatus:      StockProfileAIStatusReady,
			AIProfileUpdatedAt:   baseChangedAt.Add(time.Hour),
		},
	}, AnnouncementSyncState{
		LastSuccessAt: now.Add(-time.Hour), CoveredThrough: now.Add(-time.Hour),
	}, now)
	if !got.Ready || !got.BaseProfileReady || !got.AIProfileReady {
		t.Fatalf("unchanged checked base should remain AI-ready: %+v", got)
	}
}
