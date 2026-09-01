package stockv2

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"
)

const (
	// ponytail: two concurrent holding-only requests and one bounded stage keep
	// a provider slowdown from delaying the Agent without creating a burst that
	// is likely to hit relay QPS. This is an internal safety calibration, not an
	// owner-facing strategy setting; expose it only if real portfolios regularly
	// exceed the bound.
	portfolioSentinelFundFlowConcurrency = 2
	portfolioSentinelFundFlowTimeout     = 90 * time.Second
)

type portfolioSentinelFundFlowTarget struct {
	Symbol, Market, InstrumentType, RequiredAsOf, EndDate string
}

type portfolioSentinelFundFlowResolution struct {
	Evidence      decisionFundFlowEvidence
	Available     bool
	NotApplicable bool
	Message       string
}

// preparePortfolioSentinelFundFlow performs a holding-only preflight. Broad
// discovery remains owned by market scan; a holding does not have to rank into
// that scan to receive current decision evidence.
func (s *Service) preparePortfolioSentinelFundFlow(
	ctx context.Context,
	config OpportunityMarketScanConfig,
	targets []portfolioSentinelFundFlowTarget,
	scanEvidence map[string]OpportunityMarketScanMetrics,
) (map[string]portfolioSentinelFundFlowResolution, error) {
	out := make(map[string]portfolioSentinelFundFlowResolution, len(targets))
	pending := make([]portfolioSentinelFundFlowTarget, 0, len(targets))
	seen := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		if target.Symbol == "" {
			continue
		}
		if _, exists := seen[target.Symbol]; exists {
			continue
		}
		seen[target.Symbol] = struct{}{}
		if target.InstrumentType == InstrumentTypeExchangeFund {
			out[target.Symbol] = portfolioSentinelFundFlowResolution{NotApplicable: true}
			continue
		}
		if metrics, ok := scanEvidence[target.Symbol]; ok && metrics.FundFlowAvailable &&
			decisionFundFlowAsOfUsable(metrics.FundFlowAsOf, target.RequiredAsOf) {
			out[target.Symbol] = portfolioSentinelFundFlowResolution{
				Evidence:  decisionFundFlowEvidenceFromMetrics(target.Symbol, target.Market, metrics),
				Available: true,
			}
			continue
		}
		cached, err := s.store.GetDecisionFundFlowEvidence(ctx, target.Symbol)
		if err == nil && decisionFundFlowAsOfUsable(cached.AsOf, target.RequiredAsOf) {
			out[target.Symbol] = portfolioSentinelFundFlowResolution{Evidence: cached, Available: true}
			continue
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		pending = append(pending, target)
	}
	if len(pending) == 0 {
		return out, nil
	}

	stageCtx, cancel := context.WithTimeout(ctx, portfolioSentinelFundFlowTimeout)
	defer cancel()
	type fetchResult struct {
		target   portfolioSentinelFundFlowTarget
		evidence decisionFundFlowEvidence
		err      error
	}
	jobs := make(chan portfolioSentinelFundFlowTarget)
	results := make(chan fetchResult, len(pending))
	workerCount := min(portfolioSentinelFundFlowConcurrency, len(pending))
	var workers sync.WaitGroup
	for range workerCount {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for target := range jobs {
				fetched, err := s.fetchOpportunityMarketFundFlow(stageCtx, config, target.Symbol, target.Market,
					firstNonEmpty(target.EndDate, target.RequiredAsOf), 120)
				if err != nil {
					results <- fetchResult{target: target, err: err}
					continue
				}
				var metrics OpportunityMarketScanMetrics
				applyOpportunityFundFlow(&metrics, fetched.Points, fetched.Source)
				if !decisionFundFlowAsOfUsable(metrics.FundFlowAsOf, target.RequiredAsOf) {
					results <- fetchResult{target: target, err: errors.New("fund flow does not cover latest completed trade date")}
					continue
				}
				results <- fetchResult{target: target,
					evidence: decisionFundFlowEvidenceFromMetrics(target.Symbol, target.Market, metrics)}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, target := range pending {
			select {
			case jobs <- target:
			case <-stageCtx.Done():
				return
			}
		}
	}()
	go func() {
		workers.Wait()
		close(results)
	}()

	for result := range results {
		if result.err != nil {
			out[result.target.Symbol] = portfolioSentinelFundFlowResolution{
				Message: "持仓资金流预取失败，已尝试配置的主源与备源",
			}
			continue
		}
		if err := s.store.UpsertDecisionFundFlowEvidence(ctx, result.evidence); err != nil {
			return nil, err
		}
		out[result.target.Symbol] = portfolioSentinelFundFlowResolution{Evidence: result.evidence, Available: true}
	}
	for _, target := range pending {
		if _, ok := out[target.Symbol]; !ok {
			out[target.Symbol] = portfolioSentinelFundFlowResolution{Message: "持仓资金流预取超时"}
		}
	}
	return out, nil
}

func decisionFundFlowAsOfUsable(asOf, requiredAsOf string) bool {
	return asOf != "" && (requiredAsOf == "" || asOf >= requiredAsOf)
}

func decisionFundFlowEvidenceFromMetrics(symbol, market string, metrics OpportunityMarketScanMetrics) decisionFundFlowEvidence {
	return decisionFundFlowEvidence{
		Symbol: symbol, Market: market, AsOf: metrics.FundFlowAsOf,
		MainNetInflow5: metrics.MainNetInflow5, MainNetInflow20: metrics.MainNetInflow20,
		MainNetInflow60: metrics.MainNetInflow60, MainFlowRatio20: metrics.MainFlowRatio20,
		PositiveFlowDays20: metrics.PositiveFlowDays20, Source: metrics.FundFlowSource, FetchedAt: time.Now(),
	}
}
