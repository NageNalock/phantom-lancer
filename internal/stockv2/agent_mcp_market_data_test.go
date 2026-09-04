package stockv2

import "testing"

func TestMCPFundFlowFetchLimitPreservesAggregateWindow(t *testing.T) {
	for _, tc := range []struct {
		responseLimit int
		want          int
	}{
		{responseLimit: 8, want: 60},
		{responseLimit: 60, want: 60},
		{responseLimit: 120, want: 120},
	} {
		if got := mcpFundFlowFetchLimit(tc.responseLimit); got != tc.want {
			t.Fatalf("mcpFundFlowFetchLimit(%d) = %d, want %d", tc.responseLimit, got, tc.want)
		}
	}
}
