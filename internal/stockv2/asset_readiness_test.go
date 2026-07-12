package stockv2

import (
	"reflect"
	"testing"
	"time"
)

func TestLegacyStockV2AssetReadinessConvertsUnifiedResult(t *testing.T) {
	now := time.Now()
	got := legacyStockV2AssetReadiness(UnifiedAssetReadiness{
		MarketReady:        true,
		MessageReady:       true,
		AnalysisReady:      false,
		DailyBarReady:      true,
		BaseProfileReady:   true,
		AnnouncementReady:  true,
		AIProfileReady:     false,
		AnnouncementSyncAt: now.Add(-time.Hour),
		EvaluatedAt:        now,
		Reasons: []AssetReadinessReason{
			{Domain: "analysis", Code: "ai_input_version_outdated"},
		},
	})
	if got.Ready || !got.DataReady || !got.DailyBarReady || !got.BaseProfileReady || !got.AnnouncementReady || got.AIProfileReady {
		t.Fatalf("legacy readiness = %+v", got)
	}
	if !reflect.DeepEqual(got.Reasons, []string{"ai_input_version_outdated"}) {
		t.Fatalf("reasons = %v", got.Reasons)
	}
	if !got.EvaluatedAt.Equal(now) || !got.AnnouncementSyncAt.Equal(now.Add(-time.Hour)) {
		t.Fatalf("timestamps = %+v", got)
	}
}
