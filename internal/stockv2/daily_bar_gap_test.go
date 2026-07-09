package stockv2

import (
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
			name:  "obvious middle gap fetches only missing span",
			dates: []string{"2026-07-01", "2026-07-02", "2026-07-08", "2026-07-09", "2026-07-10"},
			want:  []dailyBarMissingRange{{Start: "2026-07-03", End: "2026-07-07"}},
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
