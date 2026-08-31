package stockv2

import (
	"encoding/json"

	"phantom-lancer/internal/safelog"
)

type portfolioSentinelPromptContextView struct {
	SchemaVersion  string                                         `json:"schemaVersion"`
	RunID          string                                         `json:"runId"`
	Window         PortfolioSentinelWindowContext                 `json:"window"`
	Portfolios     []PortfolioSentinelPortfolioContext            `json:"portfolios"`
	Candidates     []PortfolioSentinelCandidateContext            `json:"trustedCandidates,omitempty"`
	Themes         []PortfolioSentinelThemeContext                `json:"activeThemes,omitempty"`
	PriorJudgments []PortfolioSentinelPriorJudgment               `json:"priorHoldingJudgments,omitempty"`
	Transactions   []StockV2Transaction                           `json:"recentTransactions,omitempty"`
	DataFreshness  map[string]any                                 `json:"dataFreshness,omitempty"`
	ContextStats   map[string]any                                 `json:"contextStats,omitempty"`
	NewsContext    *PortfolioSentinelNewsContext                  `json:"newsContext,omitempty"`
	DecisionGates  map[string]portfolioSentinelPromptDecisionGate `json:"decisionGates,omitempty"`
	Note           string                                         `json:"note,omitempty"`
}

type portfolioSentinelPromptDecisionGate struct {
	Symbol         string                                      `json:"symbol"`
	Market         string                                      `json:"market,omitempty"`
	InstrumentType string                                      `json:"instrumentType,omitempty"`
	TradeDate      string                                      `json:"tradeDate,omitempty"`
	DecisionDate   string                                      `json:"decisionDate,omitempty"`
	Status         string                                      `json:"status"`
	MarketRegime   string                                      `json:"marketRegime,omitempty"`
	AllowedActions []string                                    `json:"allowedActions,omitempty"`
	Gates          []portfolioSentinelPromptGateResult         `json:"gates"`
	DataHealth     []portfolioSentinelPromptDecisionDataHealth `json:"dataHealth"`
	Metrics        map[string]any                              `json:"metrics,omitempty"`
}

type portfolioSentinelPromptGateResult struct {
	Key          string         `json:"key"`
	Status       string         `json:"status"`
	Summary      string         `json:"summary"`
	Reasons      []string       `json:"reasons,omitempty"`
	Metrics      map[string]any `json:"metrics,omitempty"`
	EvidenceRefs []string       `json:"evidenceRefs,omitempty"`
}

type portfolioSentinelPromptDecisionDataHealth struct {
	Key      string `json:"key"`
	Status   string `json:"status"`
	Required bool   `json:"required"`
	AsOf     string `json:"asOf,omitempty"`
	Source   string `json:"source,omitempty"`
	Message  string `json:"message,omitempty"`
}

func marshalPortfolioSentinelPromptContext(pack PortfolioSentinelContext, limit int) []byte {
	var smallest []byte
	for level := 0; level <= 3; level++ {
		compact := compactPortfolioSentinelPromptContext(pack, level)
		raw, _ := json.Marshal(compact)
		smallest = raw
		if limit <= 0 || len(raw) <= limit {
			return raw
		}
	}
	// ponytail: the 48 KiB prompt target controls routine cost, not model
	// compatibility. Preserve a complete four-holding decision pack above that
	// target; only an abnormal post-compaction payload may fall back to coverage.
	const hardContextLimit = 96 * 1024
	if len(smallest) <= hardContextLimit {
		return smallest
	}
	// ponytail: an exceptionally large portfolio must still receive valid JSON.
	// RequiredHoldingCoverage remains outside this block, so the Agent can return
	// safe holds or fetch a specific missing symbol instead of parsing a cut object.
	raw, _ := json.Marshal(map[string]any{
		"schemaVersion":    pack.SchemaVersion,
		"runId":            pack.RunID,
		"window":           pack.Window,
		"contextTruncated": true,
		"contextStats": map[string]any{
			"portfolioCount": len(pack.Portfolios),
			"holdingCount":   portfolioSentinelRequiredHoldingCount(pack),
			"reason":         "compact context still exceeded the prompt budget; use RequiredHoldingCoverage and targeted MCP retrieval",
		},
	})
	return raw
}

func compactPortfolioSentinelPromptContext(pack PortfolioSentinelContext, level int) portfolioSentinelPromptContextView {
	out := pack
	out.Note = safelog.Text(pack.Note, 500)
	out.NewsEvents = nil // Holding-scoped compact news already preserves relevance.
	out.RawNews = nil
	out.RecentReviews = nil // priorHoldingJudgments is the bounded derived-state handoff.
	out.Portfolios = make([]PortfolioSentinelPortfolioContext, 0, len(pack.Portfolios))
	for _, portfolio := range pack.Portfolios {
		portfolioOut := portfolio
		portfolioOut.Holdings = make([]PortfolioSentinelHoldingContext, 0, len(portfolio.Holdings))
		for _, holding := range portfolio.Holdings {
			holdingOut := holding
			holdingOut.RawNews = nil
			holdingOut.Profile = compactPortfolioSentinelProfile(holding.Profile, level)
			newsLimit := 3
			if level >= 1 {
				newsLimit = 1
			}
			holdingOut.News = compactPortfolioSentinelNews(holding.News, newsLimit)
			if level == 0 {
				holdingOut.NewsLinks = compactPortfolioSentinelNewsLinks(holding.NewsLinks, newsLimit)
			} else {
				holdingOut.NewsLinks = nil
			}
			portfolioOut.Holdings = append(portfolioOut.Holdings, holdingOut)
		}
		out.Portfolios = append(out.Portfolios, portfolioOut)
	}
	themeLimit := 12
	transactionLimit := 8
	if level >= 1 {
		themeLimit, transactionLimit = 6, 4
	}
	if level >= 2 {
		themeLimit, transactionLimit = 3, 0
	}
	out.Themes = append([]PortfolioSentinelThemeContext(nil), pack.Themes[:min(len(pack.Themes), themeLimit)]...)
	out.Transactions = append([]StockV2Transaction(nil), pack.Transactions[:min(len(pack.Transactions), transactionLimit)]...)
	out.ContextStats = make(map[string]any, len(pack.ContextStats)+1)
	for key, value := range pack.ContextStats {
		out.ContextStats[key] = value
	}
	out.ContextStats["promptCompactionLevel"] = level
	return portfolioSentinelPromptContextView{
		SchemaVersion: out.SchemaVersion, RunID: out.RunID, Window: out.Window,
		Portfolios: out.Portfolios, Candidates: out.Candidates, Themes: out.Themes,
		PriorJudgments: out.PriorJudgments, Transactions: out.Transactions,
		DataFreshness: out.DataFreshness, ContextStats: out.ContextStats,
		NewsContext: out.NewsContext, DecisionGates: compactPortfolioSentinelDecisionGates(pack.DecisionGates, level),
		Note: out.Note,
	}
}

func compactPortfolioSentinelProfile(profile *StockProfile, level int) *StockProfile {
	if profile == nil {
		return nil
	}
	out := *profile
	out.BusinessSummary = safelog.Text(firstNonEmpty(profile.BusinessSummaryZh, profile.BusinessSummary), 600)
	out.BusinessSummaryZh = ""
	out.BusinessSummaryEn = ""
	out.ProfileText = ""
	out.ProfileTextZh = ""
	out.ProfileTextEn = ""
	out.AliasesZh = nil
	out.AliasesEn = nil
	out.KeywordsZh = nil
	out.KeywordsEn = nil
	out.BusinessLinesEn = nil
	out.AIProfileError = safelog.Text(out.AIProfileError, 160)
	out.ConstituentHint = safelog.Text(out.ConstituentHint, 400)
	if level >= 3 {
		return &StockProfile{
			Symbol: profile.Symbol, Market: profile.Market, InstrumentType: profile.InstrumentType,
			Name: profile.Name, Industry: profile.Industry,
			Sectors: compactStringList(profile.Sectors, 3), Concepts: compactStringList(profile.Concepts, 4),
			Tags: compactStringList(profile.Tags, 3), BusinessSummary: safelog.Text(out.BusinessSummary, 240),
			BusinessLinesZh: compactStringList(profile.BusinessLinesZh, 3), RiskTagsZh: compactStringList(profile.RiskTagsZh, 3),
			AIProfileStatus: profile.AIProfileStatus, AIProfileConfidence: profile.AIProfileConfidence,
			FundType: profile.FundType, TrackingIndex: profile.TrackingIndex, Theme: profile.Theme,
			ConstituentHint: safelog.Text(profile.ConstituentHint, 240), ProfileVersion: profile.ProfileVersion,
			UpdatedAt: profile.UpdatedAt,
		}
	}
	if level >= 2 {
		out.Aliases = compactStringList(out.Aliases, 2)
		out.Sectors = compactStringList(out.Sectors, 3)
		out.Concepts = compactStringList(out.Concepts, 4)
		out.Tags = compactStringList(out.Tags, 3)
		out.BusinessLinesZh = compactStringList(out.BusinessLinesZh, 3)
		out.RiskTagsZh = compactStringList(out.RiskTagsZh, 3)
		out.BusinessSummary = safelog.Text(out.BusinessSummary, 240)
	} else {
		out.Aliases = compactStringList(out.Aliases, 4)
		out.Sectors = compactStringList(out.Sectors, 5)
		out.Concepts = compactStringList(out.Concepts, 8)
		out.Tags = compactStringList(out.Tags, 5)
		out.BusinessLinesZh = compactStringList(out.BusinessLinesZh, 6)
		out.RiskTagsZh = compactStringList(out.RiskTagsZh, 6)
	}
	return &out
}

func compactPortfolioSentinelNews(items []NewsEvent, limit int) []NewsEvent {
	if limit <= 0 || len(items) == 0 {
		return nil
	}
	out := make([]NewsEvent, 0, min(len(items), limit))
	for _, item := range items[:min(len(items), limit)] {
		item.ExternalID = ""
		item.Content = ""
		item.DedupeKey = ""
		item.Summary = safelog.Text(item.Summary, 300)
		item.Title = safelog.Text(item.Title, 240)
		out = append(out, item)
	}
	return out
}

func compactPortfolioSentinelNewsLinks(items []NewsLinkCandidate, limit int) []NewsLinkCandidate {
	if limit <= 0 || len(items) == 0 {
		return nil
	}
	out := make([]NewsLinkCandidate, 0, min(len(items), limit))
	for _, item := range items[:min(len(items), limit)] {
		item.Reason = safelog.Text(item.Reason, 180)
		item.MatchedTerms = compactStringList(item.MatchedTerms, 4)
		out = append(out, item)
	}
	return out
}

func compactPortfolioSentinelDecisionGates(items map[string]DecisionGateSnapshot, level int) map[string]portfolioSentinelPromptDecisionGate {
	if len(items) == 0 {
		return nil
	}
	out := make(map[string]portfolioSentinelPromptDecisionGate, len(items))
	for symbol, item := range items {
		gateOut := portfolioSentinelPromptDecisionGate{
			Symbol: item.Symbol, Market: item.Market, InstrumentType: item.InstrumentType,
			TradeDate: item.TradeDate, DecisionDate: item.DecisionDate, Status: item.Status,
			MarketRegime: item.MarketRegime, AllowedActions: item.AllowedActions,
			Metrics: compactPortfolioSentinelGateMetrics(item.Metrics),
		}
		gates := make([]portfolioSentinelPromptGateResult, 0, len(item.Gates))
		for _, gate := range item.Gates {
			gates = append(gates, portfolioSentinelPromptGateResult{
				Key: gate.Key, Status: gate.Status, Summary: safelog.Text(gate.Summary, 180),
				Reasons: compactStringList(gate.Reasons, 2), Metrics: gate.Metrics,
				EvidenceRefs: compactStringList(gate.EvidenceRefs, 3),
			})
		}
		gateOut.Gates = gates
		health := make([]portfolioSentinelPromptDecisionDataHealth, 0, len(item.DataHealth))
		for _, status := range item.DataHealth {
			if level >= 3 && status.Status == DecisionHealthHealthy && !status.Required {
				continue
			}
			health = append(health, portfolioSentinelPromptDecisionDataHealth{
				Key: status.Key, Status: status.Status, Required: status.Required,
				AsOf: status.AsOf, Source: status.Source, Message: safelog.Text(status.Message, 120),
			})
		}
		gateOut.DataHealth = health
		out[symbol] = gateOut
	}
	return out
}

func compactPortfolioSentinelGateMetrics(metrics map[string]any) map[string]any {
	if len(metrics) == 0 {
		return nil
	}
	allowed := map[string]struct{}{
		"atr14": {}, "atr14Pct": {}, "benchmarkReturn20Pct": {}, "factorCluster": {},
		"financialReportPeriod": {}, "lastCompletedCloseRaw": {}, "lastCompletedCloseRawDate": {},
		"lastCompletedCloseRawSource": {}, "latestCompletedTradeDate": {}, "latestTradableAmount": {},
		"latestTradableHigh": {}, "latestTradableLow": {}, "latestTradableOpen": {},
		"latestTradablePctChange": {}, "latestTradablePrevClose": {}, "latestTradablePrice": {},
		"latestTradablePriceAt": {}, "latestTradablePriceSource": {}, "latestTradableVolumeRatio": {},
		"ma20": {}, "mainFlowRatio20": {}, "netProfit": {}, "operatingCashFlow": {},
		"return20Pct": {}, "return5Pct": {}, "revenue": {}, "roe": {}, "grossMargin": {},
		"trendCloseQFQ": {},
	}
	out := make(map[string]any, len(metrics))
	for key, value := range metrics {
		if _, ok := allowed[key]; ok {
			out[key] = value
		}
	}
	return out
}

func portfolioSentinelRequiredHoldingCount(pack PortfolioSentinelContext) int {
	count := 0
	for _, portfolio := range pack.Portfolios {
		count += len(portfolio.Holdings)
	}
	return count
}
