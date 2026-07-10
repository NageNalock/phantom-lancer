package stockv2

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestPlanDailyBarMissingRanges(t *testing.T) {
	start := time.Date(2026, 7, 1, 0, 0, 0, 0, time.Local)
	end := time.Date(2026, 7, 10, 0, 0, 0, 0, time.Local)
	tests := []struct {
		name  string
		dates []string
		want  []dailyBarMissingRange
	}{
		{
			name: "no data fetches target window",
			want: []dailyBarMissingRange{{Start: "2026-07-01", End: "2026-07-10"}},
		},
		{
			name:  "tail gap starts after local latest",
			dates: []string{"2026-07-01", "2026-07-02", "2026-07-03"},
			want:  []dailyBarMissingRange{{Start: "2026-07-04", End: "2026-07-10"}},
		},
		{
			name:  "middle natural-day gap waits for exchange calendar",
			dates: []string{"2026-07-01", "2026-07-02", "2026-07-08", "2026-07-09", "2026-07-10"},
			want:  nil,
		},
		{
			name:  "complete natural window skips",
			dates: []string{"2026-07-01", "2026-07-02", "2026-07-03", "2026-07-06", "2026-07-07", "2026-07-08", "2026-07-09", "2026-07-10"},
			want:  nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := planDailyBarMissingRanges(tt.dates, start, end)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("ranges=%v, want %v", got, tt.want)
			}
		})
	}
}

func TestPlanDailyBarMissingRangesWithObservedCalendar(t *testing.T) {
	start := time.Date(2026, 7, 1, 0, 0, 0, 0, time.Local)
	end := time.Date(2026, 7, 10, 0, 0, 0, 0, time.Local)
	t.Run("repairs observed middle trading day", func(t *testing.T) {
		calendar := []string{"2026-07-01", "2026-07-02", "2026-07-03", "2026-07-06", "2026-07-07", "2026-07-08", "2026-07-09", "2026-07-10"}
		dates := []string{"2026-07-01", "2026-07-02", "2026-07-06", "2026-07-07", "2026-07-08", "2026-07-09", "2026-07-10"}
		want := []dailyBarMissingRange{{Start: "2026-07-03", End: "2026-07-03"}}
		if got := planDailyBarMissingRangesWithCalendar(dates, calendar, start, end); !reflect.DeepEqual(got, want) {
			t.Fatalf("ranges=%v, want %v", got, want)
		}
	})
	t.Run("does not request statutory closure absent from calendar", func(t *testing.T) {
		calendar := []string{"2026-07-01", "2026-07-02", "2026-07-03", "2026-07-06", "2026-07-07"}
		if got := planDailyBarMissingRangesWithCalendar(calendar, calendar, start, end); len(got) != 0 {
			t.Fatalf("ranges=%v, want no holiday request", got)
		}
	})
	t.Run("does not retry non-trading leading boundary", func(t *testing.T) {
		calendar := []string{"2026-07-02", "2026-07-03", "2026-07-06", "2026-07-07", "2026-07-08", "2026-07-09", "2026-07-10"}
		if got := planDailyBarMissingRangesWithCalendar(calendar, calendar, start, end); len(got) != 0 {
			t.Fatalf("ranges=%v, want no leading closure request", got)
		}
	})
	t.Run("fresh symbol with only closing quote keeps bootstrap envelope", func(t *testing.T) {
		calendar := []string{"2026-07-10"}
		dates := []string{"2026-07-10"}
		want := []dailyBarMissingRange{{Start: "2026-07-01", End: "2026-07-09"}}
		if got := planDailyBarMissingRangesWithCalendar(dates, calendar, start, end); !reflect.DeepEqual(got, want) {
			t.Fatalf("ranges=%v, want %v", got, want)
		}
	})
}

func TestClampDailyBarRangeToInstrumentLifecycle(t *testing.T) {
	inst := StockV2Instrument{ListDate: "20260703", DelistDate: "2026-07-08"}
	start, end := clampDailyBarRangeToInstrument(inst, "2026-07-01", "2026-07-10")
	if start != "2026-07-03" || end != "2026-07-08" {
		t.Fatalf("clamped range = %s..%s", start, end)
	}
	start, end = clampDailyBarRangeToInstrument(StockV2Instrument{ListDate: "2026-08-01"}, "2026-07-01", "2026-07-10")
	if start != "" || end != "" {
		t.Fatalf("future listing range = %s..%s, want empty", start, end)
	}
}

func TestDailyBarNegativeCoverageSubtractsOnlyStableAbsences(t *testing.T) {
	ranges := []dailyBarMissingRange{{Start: "2026-07-01", End: "2026-07-10"}}
	returned := []StockV2DailyBar{{TradeDate: "2026-07-02"}, {TradeDate: "2026-07-06"}}
	absent := subtractCheckedDailyBarRanges(ranges, []dailyBarMissingRange{
		{Start: "2026-07-01", End: "2026-07-01"},
		{Start: "2026-07-03", End: "2026-07-05"},
	})
	want := []dailyBarMissingRange{{Start: "2026-07-02", End: "2026-07-02"}, {Start: "2026-07-06", End: "2026-07-10"}}
	if !reflect.DeepEqual(absent, want) {
		t.Fatalf("remaining = %v, want %v", absent, want)
	}
	negative := stableDailyBarGapChecks(dailyBarRangesWithoutReturnedBars(ranges, returned), time.Date(2026, 7, 10, 12, 0, 0, 0, chinaMarketTZ))
	if len(negative) == 0 || negative[len(negative)-1].End > "2026-07-07" {
		t.Fatalf("negative coverage = %v, want capped at T-3", negative)
	}
}

func TestDailyBarGapChecksPersistAcrossRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "stockv2.db")
	store, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []dailyBarMissingRange{{Start: "2026-06-01", End: "2026-06-05"}}
	if err := store.RecordDailyBarGapChecks(ctx, "600000", DailyBarAdjustedNone, want); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	got, err := store.ListDailyBarGapChecks(ctx, "600000", DailyBarAdjustedNone, "2026-06-01", "2026-06-30")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("persisted gap checks = %v, want %v", got, want)
	}
}

func TestDailyBarGapChecksExpire(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(filepath.Join(t.TempDir(), "stockv2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.RecordDailyBarGapChecks(ctx, "600000", DailyBarAdjustedNone, []dailyBarMissingRange{{Start: "2026-06-01", End: "2026-06-05"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `
		UPDATE stockv2_daily_bar_gap_checks SET checked_at = ? WHERE symbol = ?
	`, time.Now().Add(-dailyBarGapCheckTTL-time.Hour), "600000"); err != nil {
		t.Fatal(err)
	}
	got, err := store.ListDailyBarGapChecks(ctx, "600000", DailyBarAdjustedNone, "2026-06-01", "2026-06-30")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("expired negative coverage still active: %v", got)
	}
}
