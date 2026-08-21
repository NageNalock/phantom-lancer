package stockv2

import (
	"context"
	"sort"
	"strings"
	"time"

	"phantom-lancer/internal/safelog"
)

const (
	// ponytail: message-driven admission is a small bounded lane inside the existing
	// daily scan. A 72-hour window covers late-night and weekend catalysts without
	// turning old themes into permanent candidate boosts.
	opportunityMarketScanThemeLookback          = 72 * time.Hour
	opportunityMarketScanThemeMinimumConfidence = 0.40
	opportunityMarketScanThemeSemanticMinScore  = 0.45
)

func (s *Service) hydrateOpportunityMarketScanProfiles(ctx context.Context, raw []opportunityMarketScanRawMetric) (map[string]StockProfile, error) {
	profiles, profileErr := s.listAllStockProfiles(ctx)
	bySymbol := make(map[string]StockProfile, len(profiles))
	for _, profile := range profiles {
		bySymbol[strings.TrimSpace(profile.Symbol)] = profile
	}
	for i := range raw {
		profile, ok := bySymbol[raw[i].Instrument.Symbol]
		if !ok {
			profile = buildStockProfileFromInstrument(raw[i].Instrument)
			bySymbol[raw[i].Instrument.Symbol] = profile
		}
		if raw[i].Instrument.Name == "" {
			raw[i].Instrument.Name = profile.Name
		}
		if raw[i].Instrument.Industry == "" {
			raw[i].Instrument.Industry = profile.Industry
		}
		if raw[i].Instrument.Sector == "" && len(profile.Sectors) > 0 {
			raw[i].Instrument.Sector = profile.Sectors[0]
		}
		raw[i].Instrument.Concepts = appendProfileTerms(raw[i].Instrument.Concepts, profile.Concepts...)
	}
	return bySymbol, profileErr
}

func (s *Service) loadOpportunityMarketThemeMatches(
	ctx context.Context,
	profiles map[string]StockProfile,
	scored []opportunityMarketScanRawMetric,
	at time.Time,
) (map[string][]OpportunityMarketThemeMatch, OpportunityMarketThemeSnapshot) {
	if at.IsZero() {
		at = time.Now()
	}
	since := at.Add(-opportunityMarketScanThemeLookback)
	snapshot := OpportunityMarketThemeSnapshot{CapturedAt: at, Since: since, Status: DecisionHealthNotApplicable}
	material := true
	versions, err := s.store.ListNewsThreadVersions(ctx, NewsThreadVersionListFilter{
		MaterialChange: &material, Since: since, Until: at.Add(time.Nanosecond), Limit: 50,
	})
	if err != nil {
		snapshot.Status = DecisionHealthDegraded
		snapshot.Message = "消息主题版本读取失败：" + safelog.Error(err, 160)
		return map[string][]OpportunityMarketThemeMatch{}, snapshot
	}
	active := make(map[string]NewsThread)
	for _, thread := range s.activeOpportunityMarketScanThreads(ctx) {
		active[thread.ID] = thread
	}
	selected := make([]NewsThreadVersion, 0, opportunityMarketScanThemeVersionLimit)
	seenThread := make(map[string]struct{})
	for _, version := range versions {
		if len(selected) >= opportunityMarketScanThemeVersionLimit {
			break
		}
		thread, ok := active[version.ThreadID]
		if !ok || !opportunityMarketThemeStageAdmissible(thread.Stage) ||
			!opportunityMarketThemeStageAdmissible(version.Stage) ||
			thread.Confidence < opportunityMarketScanThemeMinimumConfidence || version.Confidence < opportunityMarketScanThemeMinimumConfidence {
			continue
		}
		if _, ok := seenThread[version.ThreadID]; ok {
			continue
		}
		seenThread[version.ThreadID] = struct{}{}
		selected = append(selected, version)
		snapshot.VersionIDs = append(snapshot.VersionIDs, version.ID)
	}
	snapshot.VersionCount = len(selected)
	if len(selected) == 0 {
		snapshot.Message = "最近 72 小时没有可用于新机会召回的实质变化主题"
		return map[string][]OpportunityMarketThemeMatch{}, snapshot
	}

	eligible := make(map[string]struct{}, len(scored))
	for _, item := range scored {
		eligible[item.Instrument.Symbol] = struct{}{}
	}
	matches := make(map[string][]OpportunityMarketThemeMatch)
	for _, version := range selected {
		for symbol, profile := range profiles {
			if _, ok := eligible[symbol]; !ok {
				continue
			}
			if match, ok := deterministicOpportunityMarketThemeMatch(version, profile); ok {
				matches[symbol] = upsertOpportunityMarketThemeMatch(matches[symbol], match)
			}
		}
	}

	embeddingStatus, embeddingErr := s.GetEmbeddingStatus(ctx)
	snapshot.SemanticAvailable = embeddingErr == nil && embeddingStatus.Available
	if snapshot.SemanticAvailable {
		for _, version := range selected {
			snapshot.SemanticQueryCount++
			hits, searchErr := s.SemanticSearchStockProfiles(ctx, SemanticSearchRequest{
				Query: opportunityMarketThemeQuery(version), Limit: opportunityMarketScanThemeSemanticLimit,
				MinScore: opportunityMarketScanThemeSemanticMinScore,
			})
			if searchErr != nil {
				snapshot.SemanticFailureCount++
				continue
			}
			for _, hit := range hits {
				symbol := strings.TrimSpace(hit.Profile.Symbol)
				if _, ok := eligible[symbol]; !ok {
					continue
				}
				match := opportunityMarketThemeMatch(version, OpportunityMarketThemeMatchSemantic, nil)
				match.SemanticScore = hit.Score
				match.RequiresCausalVerification = true
				matches[symbol] = upsertOpportunityMarketThemeMatch(matches[symbol], match)
			}
		}
	}
	snapshot.MatchedCandidateCount = len(matches)
	switch {
	case !snapshot.SemanticAvailable:
		snapshot.Status = DecisionHealthDegraded
		snapshot.Message = "画像向量召回不可用，已保留代码与画像关键词匹配"
	case snapshot.SemanticFailureCount > 0:
		snapshot.Status = DecisionHealthDegraded
		snapshot.Message = "部分主题向量召回失败，候选结果已降级"
	default:
		snapshot.Status = DecisionHealthHealthy
		snapshot.Message = "实质变化主题已完成代码、画像关键词与向量召回"
	}
	return matches, snapshot
}

func opportunityMarketThemeStageAdmissible(stage string) bool {
	switch strings.TrimSpace(stage) {
	case NewsThreadStageEmerging, NewsThreadStageSpreading, NewsThreadStageAccelerating, NewsThreadStageRestarting:
		return true
	default:
		return false
	}
}

func deterministicOpportunityMarketThemeMatch(version NewsThreadVersion, profile StockProfile) (OpportunityMarketThemeMatch, bool) {
	directReferences := append([]string{}, version.Symbols...)
	directReferences = append(directReferences, version.Leaders...)
	directReferences = append(directReferences, version.Followers...)
	directReferences = append(directReferences, version.NextCandidates...)
	directTerms := append([]string{profile.Symbol, profile.Name}, profile.Aliases...)
	for _, reference := range directReferences {
		for _, term := range directTerms {
			if sameNewsTerm(reference, term) {
				match := opportunityMarketThemeMatch(version, OpportunityMarketThemeMatchDirect, []string{term})
				match.RequiresCausalVerification = false
				return match, true
			}
		}
	}

	text := normalizeNewsMatchText(opportunityMarketThemeQuery(version))
	structured := []string{profile.Industry, profile.Theme}
	structured = append(structured, profile.Sectors...)
	structured = append(structured, profile.Concepts...)
	structured = append(structured, profile.Tags...)
	if terms := opportunityMarketThemeMatchedTerms(text, structured); len(terms) > 0 {
		return opportunityMarketThemeMatch(version, OpportunityMarketThemeMatchStructured, terms), true
	}
	keywords := append([]string{}, profile.KeywordsZh...)
	keywords = append(keywords, profile.BusinessLinesZh...)
	if terms := opportunityMarketThemeMatchedTerms(text, keywords); len(terms) > 0 {
		return opportunityMarketThemeMatch(version, OpportunityMarketThemeMatchKeyword, terms), true
	}
	return OpportunityMarketThemeMatch{}, false
}

func opportunityMarketThemeMatchedTerms(text string, candidates []string) []string {
	var out []string
	for _, term := range cleanProfileTerms(candidates) {
		if !usefulNewsTerm(term) || !strings.Contains(text, strings.ToLower(term)) {
			continue
		}
		out = append(out, term)
		if len(out) >= 3 {
			break
		}
	}
	return out
}

func opportunityMarketThemeMatch(version NewsThreadVersion, kind string, terms []string) OpportunityMarketThemeMatch {
	return OpportunityMarketThemeMatch{
		ThreadID: version.ThreadID, VersionID: version.ID, Title: version.Title,
		Stage: version.Stage, Confidence: version.Confidence, MaterialChange: version.MaterialChange,
		EffectiveAt: firstNonZeroTime(version.EffectiveAt, version.CreatedAt), MatchKind: kind,
		MatchedTerms: terms, RequiresCausalVerification: kind != OpportunityMarketThemeMatchDirect,
	}
}

func upsertOpportunityMarketThemeMatch(items []OpportunityMarketThemeMatch, incoming OpportunityMarketThemeMatch) []OpportunityMarketThemeMatch {
	for i := range items {
		if items[i].VersionID != incoming.VersionID {
			continue
		}
		if opportunityMarketThemeMatchPriority(incoming.MatchKind) > opportunityMarketThemeMatchPriority(items[i].MatchKind) {
			semanticScore := items[i].SemanticScore
			items[i] = incoming
			items[i].SemanticScore = max(items[i].SemanticScore, semanticScore)
		} else {
			items[i].SemanticScore = max(items[i].SemanticScore, incoming.SemanticScore)
		}
		return items
	}
	return append(items, incoming)
}

func opportunityMarketThemeMatchPriority(kind string) int {
	switch kind {
	case OpportunityMarketThemeMatchDirect:
		return 4
	case OpportunityMarketThemeMatchStructured:
		return 3
	case OpportunityMarketThemeMatchKeyword:
		return 2
	case OpportunityMarketThemeMatchSemantic:
		return 1
	default:
		return 0
	}
}

func opportunityMarketThemeQuery(version NewsThreadVersion) string {
	parts := []string{version.Title, version.CoreThesis, version.LatestChange}
	parts = append(parts, version.Industries...)
	parts = append(parts, version.Facts...)
	parts = append(parts, version.Inferences...)
	parts = append(parts, version.Leaders...)
	parts = append(parts, version.Followers...)
	parts = append(parts, version.NextCandidates...)
	parts = append(parts, version.Catalysts...)
	return safelog.Text(strings.Join(nonEmptyStrings(parts), " "), 1400)
}

func selectOpportunityMarketPrefilter(
	scored []opportunityMarketScanRawMetric,
	matches map[string][]OpportunityMarketThemeMatch,
) ([]opportunityMarketScanRawMetric, map[string]int) {
	rankBySymbol := make(map[string]int, len(scored))
	bySymbol := make(map[string]opportunityMarketScanRawMetric, len(scored))
	messageSymbols := make([]string, 0, len(matches))
	for i, item := range scored {
		symbol := item.Instrument.Symbol
		rankBySymbol[symbol] = i + 1
		bySymbol[symbol] = item
		if len(matches[symbol]) > 0 {
			messageSymbols = append(messageSymbols, symbol)
		}
	}
	sort.SliceStable(messageSymbols, func(i, j int) bool {
		left, right := opportunityMarketThemeMatchesPriority(matches[messageSymbols[i]]), opportunityMarketThemeMatchesPriority(matches[messageSymbols[j]])
		if left != right {
			return left > right
		}
		return rankBySymbol[messageSymbols[i]] < rankBySymbol[messageSymbols[j]]
	})
	selected := make([]opportunityMarketScanRawMetric, 0, min(len(scored), opportunityMarketScanLocalLimit))
	seen := make(map[string]struct{}, opportunityMarketScanLocalLimit)
	for _, symbol := range messageSymbols[:min(len(messageSymbols), opportunityMarketScanMessageLocalReserve)] {
		selected = append(selected, bySymbol[symbol])
		seen[symbol] = struct{}{}
	}
	for _, item := range scored {
		if len(selected) >= opportunityMarketScanLocalLimit {
			break
		}
		if _, ok := seen[item.Instrument.Symbol]; ok {
			continue
		}
		selected = append(selected, item)
		seen[item.Instrument.Symbol] = struct{}{}
	}
	return selected, rankBySymbol
}

func opportunityMarketThemeMatchesPriority(matches []OpportunityMarketThemeMatch) int {
	priority := 0
	for _, match := range matches {
		priority = max(priority, opportunityMarketThemeMatchPriority(match.MatchKind))
	}
	return priority
}

func opportunityMarketSourceLane(prefilterRank int, matches []OpportunityMarketThemeMatch) string {
	if len(matches) == 0 {
		return OpportunityMarketScanSourcePrice
	}
	if prefilterRank > opportunityMarketScanLocalLimit {
		return OpportunityMarketScanSourceMessage
	}
	return OpportunityMarketScanSourceMixed
}

func reserveOpportunityMarketMessageResearch(candidates []OpportunityMarketScanCandidate) []OpportunityMarketScanCandidate {
	if len(candidates) <= opportunityMarketScanResearchLimit {
		return candidates
	}
	selected := make(map[string]struct{}, opportunityMarketScanResearchLimit)
	for _, candidate := range candidates {
		if len(candidate.Metrics.ThemeMatches) == 0 {
			continue
		}
		selected[candidate.Symbol] = struct{}{}
		if len(selected) >= opportunityMarketScanMessageResearchReserve {
			break
		}
	}
	for _, candidate := range candidates {
		if len(selected) >= opportunityMarketScanResearchLimit {
			break
		}
		selected[candidate.Symbol] = struct{}{}
	}
	ordered := make([]OpportunityMarketScanCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if _, ok := selected[candidate.Symbol]; ok {
			ordered = append(ordered, candidate)
		}
	}
	for _, candidate := range candidates {
		if _, ok := selected[candidate.Symbol]; !ok {
			ordered = append(ordered, candidate)
		}
	}
	return ordered
}

func opportunityMarketThemeScoreFromMatches(matches []OpportunityMarketThemeMatch, catalystCutoff time.Time) (float64, []string, []string) {
	score := 0.0
	var signals, catalysts []string
	for _, match := range matches {
		switch match.MatchKind {
		case OpportunityMarketThemeMatchDirect:
			score = max(score, 100)
		case OpportunityMarketThemeMatchStructured:
			score = max(score, 55)
		case OpportunityMarketThemeMatchKeyword:
			score = max(score, 35)
		}
		if len(signals) < 5 && !stringListContainsFold(signals, match.Title) {
			signals = append(signals, match.Title)
		}
		if !catalystCutoff.IsZero() && !match.EffectiveAt.IsZero() && !match.EffectiveAt.Before(catalystCutoff) &&
			len(catalysts) < 5 && !stringListContainsFold(catalysts, match.Title) {
			catalysts = append(catalysts, match.Title)
		}
	}
	return score, signals, catalysts
}

func opportunityMarketThemeDataHealth(matches []OpportunityMarketThemeMatch, now time.Time) *DecisionDataHealth {
	if len(matches) == 0 {
		return nil
	}
	status := DecisionHealthHealthy
	message := "主题明确提及该标的，仍需 Agent 核验事实与定价"
	for _, match := range matches {
		if match.RequiresCausalVerification {
			status = DecisionHealthDegraded
			message = "主题通过画像或语义召回关联，必须核验真实业务暴露与传导路径"
			break
		}
	}
	return &DecisionDataHealth{
		Key: "theme_causality", Label: "消息主题因果映射", Status: status, Required: false,
		Message: message, CheckedAt: now,
	}
}

func opportunityMarketProfileDataHealth(candidate OpportunityMarketScanCandidate, now time.Time) *DecisionDataHealth {
	if strings.TrimSpace(candidate.Industry) != "" || strings.TrimSpace(candidate.Sector) != "" || len(candidate.Concepts) > 0 {
		return nil
	}
	return &DecisionDataHealth{
		Key: "profile_semantics", Label: "股票画像结构字段", Status: DecisionHealthDegraded, Required: false,
		Message: "行业与概念结构字段缺失，已保留画像关键词和向量召回，但因果映射可信度降低", CheckedAt: now,
	}
}

func (s *Service) shouldStartOpportunityMarketThemeRefresh(ctx context.Context, tradeDate string, at time.Time) bool {
	if strings.TrimSpace(tradeDate) == "" {
		return false
	}
	runs, err := s.store.ListOpportunityMarketScanRuns(ctx, OpportunityMarketScanRunListFilter{Limit: 30})
	if err != nil {
		return false
	}
	var base *OpportunityMarketScanRun
	for i := range runs {
		if runs[i].TradeDate != tradeDate {
			continue
		}
		if runs[i].TriggerType == OpportunityMarketScanTriggerThemeRefresh {
			// ponytail: one late-theme refresh per trading date bounds provider and
			// model work; the refresh run's normal retry budget handles failures.
			return false
		}
		if base == nil && (runs[i].Status == OpportunityMarketScanStatusCompleted || runs[i].Status == OpportunityMarketScanStatusPartial) {
			candidate := runs[i]
			base = &candidate
		}
	}
	if base == nil || base.ThemeSnapshot.CapturedAt.IsZero() {
		return false
	}
	material := true
	versions, err := s.store.ListNewsThreadVersions(ctx, NewsThreadVersionListFilter{
		MaterialChange: &material, Since: base.ThemeSnapshot.CapturedAt, Until: at.Add(time.Nanosecond), Limit: 50,
	})
	if err != nil || len(versions) == 0 {
		return false
	}
	active := make(map[string]NewsThread)
	for _, thread := range s.activeOpportunityMarketScanThreads(ctx) {
		active[thread.ID] = thread
	}
	consumed := make(map[string]struct{}, len(base.ThemeSnapshot.VersionIDs))
	for _, id := range base.ThemeSnapshot.VersionIDs {
		consumed[id] = struct{}{}
	}
	for _, version := range versions {
		effectiveAt := firstNonZeroTime(version.EffectiveAt, version.CreatedAt)
		if !effectiveAt.After(base.ThemeSnapshot.CapturedAt) || version.Confidence < opportunityMarketScanThemeMinimumConfidence ||
			!opportunityMarketThemeStageAdmissible(version.Stage) {
			continue
		}
		thread, ok := active[version.ThreadID]
		if !ok || !opportunityMarketThemeStageAdmissible(thread.Stage) || thread.Confidence < opportunityMarketScanThemeMinimumConfidence {
			continue
		}
		if _, ok := consumed[version.ID]; !ok {
			return true
		}
	}
	return false
}

func (s *Service) opportunityMarketScanCachedMetrics(ctx context.Context, current OpportunityMarketScanRun) map[string]OpportunityMarketScanMetrics {
	runs, err := s.store.ListOpportunityMarketScanRuns(ctx, OpportunityMarketScanRunListFilter{Limit: 30})
	if err != nil {
		return map[string]OpportunityMarketScanMetrics{}
	}
	for _, run := range runs {
		if run.ID == current.ID || run.TradeDate != current.TradeDate ||
			(run.Status != OpportunityMarketScanStatusCompleted && run.Status != OpportunityMarketScanStatusPartial) {
			continue
		}
		items, listErr := s.store.ListOpportunityMarketScanCandidates(ctx, OpportunityMarketScanCandidateListFilter{
			ScanRunID: run.ID, Limit: opportunityMarketScanLocalLimit,
		})
		if listErr != nil {
			return map[string]OpportunityMarketScanMetrics{}
		}
		out := make(map[string]OpportunityMarketScanMetrics, len(items))
		for _, item := range items {
			out[item.Symbol] = item.Metrics
		}
		return out
	}
	return map[string]OpportunityMarketScanMetrics{}
}

func copyOpportunityFundFlowMetrics(target *OpportunityMarketScanMetrics, source OpportunityMarketScanMetrics) {
	target.MainNetInflow5 = source.MainNetInflow5
	target.MainNetInflow20 = source.MainNetInflow20
	target.MainNetInflow60 = source.MainNetInflow60
	target.MainFlowRatio20 = source.MainFlowRatio20
	target.PositiveFlowDays20 = source.PositiveFlowDays20
	target.FundFlowAvailable = source.FundFlowAvailable
	target.FundFlowStatus = source.FundFlowStatus
	target.FundFlowSource = source.FundFlowSource
	target.FundFlowAsOf = source.FundFlowAsOf
}
