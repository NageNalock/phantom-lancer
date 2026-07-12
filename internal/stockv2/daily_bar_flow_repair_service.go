package stockv2

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"phantom-lancer/internal/safelog"
)

const (
	dailyBarFlowRepairCursorScope = "daily_bar_flow_repair_cursor"
	dailyBarFlowRepairBudgetScope = "daily_bar_flow_repair_budget"
	dailyBarFlowRepairBatchSize   = 10
	dailyBarFlowPriorityQuota     = 4
	dailyBarFlowRepairDailyLimit  = 300
	dailyBarFlowRepairInterval    = 5 * time.Minute
)

func (s *Service) runDailyBarFlowRepairScheduler(ctx context.Context) {
	ticker := time.NewTicker(dailyBarFlowRepairInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			attempted, err := s.repairDailyBarFlowBatch(ctx, now)
			if err != nil && s.log != nil {
				s.log.Warn("daily bar flow repair batch stopped", "attempted", attempted, "error", safelog.Text(err.Error(), 240))
			}
		}
	}
}

func (s *Service) repairDailyBarFlowBatch(ctx context.Context, now time.Time) (int, error) {
	if s == nil || s.store == nil {
		return 0, nil
	}
	if gate := s.currentResourceGate(); gate.State != ResourceGateNormal {
		return 0, nil
	}
	day, used := parseDailyBarFlowRepairBudget("", now)
	if stored, err := s.store.GetAssetMaintenanceCursor(ctx, dailyBarFlowRepairBudgetScope); err != nil {
		return 0, err
	} else {
		day, used = parseDailyBarFlowRepairBudget(stored, now)
	}
	remaining := dailyBarFlowRepairDailyLimit - used
	if remaining <= 0 {
		return 0, nil
	}
	qualities, err := s.store.ListDailyBarFlowGapCoverage(ctx, 10000)
	if err != nil || len(qualities) == 0 {
		return 0, err
	}
	qualityBySymbol := make(map[string]DailyBarCoverageQuality, len(qualities))
	all := make([]string, 0, len(qualities))
	for _, quality := range qualities {
		qualityBySymbol[quality.Symbol] = quality
		all = append(all, quality.Symbol)
	}
	sort.Strings(all)
	retryStates, err := s.store.ListDailyBarFlowRepairStates(ctx)
	if err != nil {
		return 0, err
	}
	cursor, err := s.store.GetAssetMaintenanceCursor(ctx, dailyBarFlowRepairCursorScope)
	if err != nil {
		return 0, err
	}
	rotated := rotateSymbolsAfterCursor(all, cursor)
	priority := make([]string, 0)
	if holdings, loadErr := s.store.ListHoldingSymbols(ctx); loadErr == nil {
		priority = append(priority, holdings...)
	}
	if strategySymbols, loadErr := s.activeStrategySymbols(ctx); loadErr == nil {
		strategies := make([]string, 0, len(strategySymbols))
		for symbol := range strategySymbols {
			strategies = append(strategies, symbol)
		}
		sort.Strings(strategies)
		priority = append(priority, strategies...)
	}
	ordered := orderDailyBarFlowRepairCandidates(priority, rotated, qualityBySymbol, retryStates, now)
	limit := min(dailyBarFlowRepairBatchSize, remaining, len(ordered))
	if limit == 0 {
		return 0, nil
	}
	instruments, err := s.store.GetInstrumentsBySymbols(ctx, ordered[:limit])
	if err != nil {
		return 0, err
	}
	instrumentBySymbol := make(map[string]StockV2Instrument, len(instruments))
	for _, instrument := range instruments {
		instrumentBySymbol[instrument.Symbol] = instrument
	}
	attempted := 0
	for _, symbol := range ordered[:limit] {
		if ctx.Err() != nil {
			return attempted, ctx.Err()
		}
		quality := qualityBySymbol[symbol]
		instrument, exists := instrumentBySymbol[symbol]
		if !exists {
			state := retryStates[symbol]
			state.Symbol = symbol
			state.AttemptCount++
			state.LastAttemptAt = now
			state.NextRetryAt = now.Add(dailyBarFlowRetryDelay(state.AttemptCount))
			state.LastError = "instrument is unavailable"
			if err := s.store.UpsertDailyBarFlowRepairState(ctx, state, now); err != nil {
				return attempted, err
			}
			if err := s.store.SetAssetMaintenanceCursor(ctx, dailyBarFlowRepairCursorScope, symbol); err != nil {
				return attempted, err
			}
			continue
		}
		if until, blocked := s.assetSourceBackoffUntil(eastmoneyDailyFlowBackoffKey, now); blocked {
			state := retryStates[symbol]
			state.Symbol = symbol
			state.NextRetryAt = until
			state.LastError = "flow provider cooldown"
			if err := s.store.UpsertDailyBarFlowRepairState(ctx, state, now); err != nil {
				return attempted, err
			}
			if err := s.store.SetAssetMaintenanceCursor(ctx, dailyBarFlowRepairCursorScope, symbol); err != nil {
				return attempted, err
			}
			continue
		}
		// ponytail: reserve the request budget before touching the network. A
		// crash may conservatively waste one slot, but it can never make the
		// process exceed the hard 300-request daily ceiling after restart.
		used++
		if err := s.store.SetAssetMaintenanceCursor(
			ctx, dailyBarFlowRepairBudgetScope, formatDailyBarFlowRepairBudget(day, used),
		); err != nil {
			return attempted, err
		}
		requested, repairErr := s.repairStoredDailyBarFlowFacets(
			ctx, instrument, quality.WindowStartDate, quality.WindowEndDate,
		)
		if requested {
			attempted++
		} else {
			used--
			if err := s.store.SetAssetMaintenanceCursor(
				ctx, dailyBarFlowRepairBudgetScope, formatDailyBarFlowRepairBudget(day, used),
			); err != nil {
				return attempted, err
			}
		}
		refreshed, refreshErr := s.store.RefreshDailyBarCoverageQuality(
			ctx, instrument, quality.Adjusted, quality.WindowStartDate, quality.WindowEndDate,
		)
		if refreshErr != nil && repairErr == nil {
			repairErr = refreshErr
		}
		// Cursor movement follows the persisted repair/quality attempt, never an
		// in-memory selection, so restart resumes after the last durable symbol.
		if err := s.store.SetAssetMaintenanceCursor(ctx, dailyBarFlowRepairCursorScope, symbol); err != nil {
			return attempted, err
		}
		if repairErr != nil || refreshed.FlowGapCount > 0 {
			state := retryStates[symbol]
			state.Symbol = symbol
			state.AttemptCount++
			state.LastAttemptAt = now
			state.NextRetryAt = now.Add(dailyBarFlowRetryDelay(state.AttemptCount))
			if repairErr != nil {
				state.LastError = repairErr.Error()
			} else {
				state.LastError = "flow facets remain incomplete"
			}
			if err := s.store.UpsertDailyBarFlowRepairState(ctx, state, now); err != nil {
				return attempted, err
			}
			if repairErr != nil {
				return attempted, repairErr
			}
		} else if err := s.store.DeleteDailyBarFlowRepairState(ctx, symbol); err != nil {
			return attempted, err
		}
	}
	return attempted, nil
}

func dailyBarFlowRetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	return min(15*time.Minute*time.Duration(1<<min(attempt-1, 5)), 12*time.Hour)
}

func orderDailyBarFlowRepairCandidates(
	priority []string,
	rotated []string,
	qualityBySymbol map[string]DailyBarCoverageQuality,
	retryStates map[string]dailyBarFlowRepairState,
	now time.Time,
) []string {
	ordered := make([]string, 0, len(rotated))
	seen := make(map[string]struct{}, len(rotated))
	appendCandidate := func(symbol string) {
		if _, exists := qualityBySymbol[symbol]; !exists {
			return
		}
		if _, exists := seen[symbol]; exists {
			return
		}
		if state, exists := retryStates[symbol]; exists && state.NextRetryAt.After(now) {
			return
		}
		seen[symbol] = struct{}{}
		ordered = append(ordered, symbol)
	}
	priority = compactStringList(priority, len(priority))
	prioritySet := make(map[string]struct{}, len(priority))
	for _, symbol := range priority {
		prioritySet[symbol] = struct{}{}
	}
	prioritySplit := min(len(priority), dailyBarFlowPriorityQuota)
	for _, symbol := range priority[:prioritySplit] {
		appendCandidate(symbol)
	}
	for _, symbol := range rotated {
		if _, isPriority := prioritySet[symbol]; isPriority {
			continue
		}
		appendCandidate(symbol)
	}
	for _, symbol := range priority[prioritySplit:] {
		appendCandidate(symbol)
	}
	return ordered
}

func parseDailyBarFlowRepairBudget(value string, now time.Time) (string, int) {
	loc := chinaMarketTZ
	day := now.In(loc).Format("2006-01-02")
	parts := strings.Split(strings.TrimSpace(value), "|")
	if len(parts) != 2 || parts[0] != day {
		return day, 0
	}
	used, err := strconv.Atoi(parts[1])
	if err != nil || used < 0 {
		return day, 0
	}
	return day, min(used, dailyBarFlowRepairDailyLimit)
}

func formatDailyBarFlowRepairBudget(day string, used int) string {
	return fmt.Sprintf("%s|%d", day, used)
}
