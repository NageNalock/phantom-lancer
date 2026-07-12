package stockv2

import (
	"fmt"
	"testing"
	"time"
)

func TestDailyBarFlowRepairBudgetIsDailyAndBounded(t *testing.T) {
	now := time.Date(2026, 7, 12, 9, 0, 0, 0, chinaMarketTZ)
	day, used := parseDailyBarFlowRepairBudget("2026-07-12|42", now)
	if day != "2026-07-12" || used != 42 {
		t.Fatalf("same-day budget = %s %d", day, used)
	}
	_, used = parseDailyBarFlowRepairBudget("2026-07-11|299", now)
	if used != 0 {
		t.Fatalf("previous-day budget = %d, want 0", used)
	}
	_, used = parseDailyBarFlowRepairBudget("2026-07-12|9999", now)
	if used != dailyBarFlowRepairDailyLimit {
		t.Fatalf("clamped budget = %d, want %d", used, dailyBarFlowRepairDailyLimit)
	}
	if got := formatDailyBarFlowRepairBudget(day, 43); got != "2026-07-12|43" {
		t.Fatalf("formatted budget = %q", got)
	}
}

func TestDailyBarFlowRepairPriorityQuotaPreservesCursorFairness(t *testing.T) {
	now := time.Date(2026, 7, 12, 9, 0, 0, 0, chinaMarketTZ)
	priority := make([]string, 0, 10)
	rotated := make([]string, 0, 20)
	qualities := make(map[string]DailyBarCoverageQuality)
	for i := 0; i < 10; i++ {
		symbol := fmt.Sprintf("P%02d", i)
		priority = append(priority, symbol)
		rotated = append(rotated, symbol)
		qualities[symbol] = DailyBarCoverageQuality{Symbol: symbol, FlowGapCount: 1}
	}
	for i := 0; i < 10; i++ {
		symbol := fmt.Sprintf("N%02d", i)
		rotated = append(rotated, symbol)
		qualities[symbol] = DailyBarCoverageQuality{Symbol: symbol, FlowGapCount: 1}
	}
	states := map[string]dailyBarFlowRepairState{
		"P00": {Symbol: "P00", NextRetryAt: now.Add(time.Hour)},
	}
	got := orderDailyBarFlowRepairCandidates(priority, rotated, qualities, states, now)
	if len(got) < dailyBarFlowRepairBatchSize {
		t.Fatalf("ordered candidates = %v", got)
	}
	priorityCount := 0
	for _, symbol := range got[:dailyBarFlowRepairBatchSize] {
		if symbol[0] == 'P' {
			priorityCount++
		}
	}
	if priorityCount > dailyBarFlowPriorityQuota {
		t.Fatalf("first batch priority count = %d, ordered=%v", priorityCount, got[:dailyBarFlowRepairBatchSize])
	}
	if got[0] == "P00" {
		t.Fatalf("future retry state was ignored: %v", got)
	}
}
