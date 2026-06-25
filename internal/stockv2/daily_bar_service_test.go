package stockv2

import (
	"context"
	"testing"
)

func TestDailyBarsNeedsMaintenance(t *testing.T) {
	tests := []struct {
		name string
		q    DailyBarsQuality
		want bool
	}{
		{name: "missing", q: DailyBarsQuality{}, want: true},
		{name: "partial", q: DailyBarsQuality{HasData: true, RowCount: 120, Meets250: false}, want: true},
		{name: "stale", q: DailyBarsQuality{HasData: true, RowCount: 260, Meets250: true, Stale: true}, want: true},
		{name: "ready", q: DailyBarsQuality{HasData: true, RowCount: 260, Meets250: true, Stale: false}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := dailyBarsNeedsMaintenance(tt.q); got != tt.want {
				t.Fatalf("dailyBarsNeedsMaintenance() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLegacyDailyBarsAutoMigratesToUnifiedMaintenance(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	if err := svc.Initialize(ctx); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	settings := svc.settings
	settings.AutoUpdateEnabled = false
	settings.DailyBarsAutoEnabled = true
	if err := svc.store.CreateOrUpdateSettings(ctx, settings); err != nil {
		t.Fatalf("save legacy settings: %v", err)
	}

	got, err := svc.GetSettings(ctx)
	if err != nil {
		t.Fatalf("get settings: %v", err)
	}
	if !got.AutoUpdateEnabled {
		t.Fatalf("AutoUpdateEnabled = false, want migrated true")
	}
	if got.DailyBarsAutoEnabled {
		t.Fatalf("DailyBarsAutoEnabled = true, want legacy field cleared")
	}
}
