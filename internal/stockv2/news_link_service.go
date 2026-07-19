package stockv2

import (
	"context"
	"sort"
	"strings"
	"time"

	"phantom-lancer/internal/safelog"
)

const (
	newsScoreExactSymbol    = 100
	newsScoreExactName      = 95
	newsScoreAlias          = 85
	newsScoreKeyword        = 55
	newsScoreProfileKeyword = 40
	newsBoostHolding        = 10
	newsBoostActiveStrategy = 8
	newsSemanticMinScore    = 0.45
	newsProfileEnglishMin   = 4
)

func (s *Service) CreateNewsEvent(ctx context.Context, event NewsEvent) (NewsEvent, error) {
	return s.store.CreateNewsEvent(ctx, event)
}

func (s *Service) LinkNewsEvent(ctx context.Context, eventID string) ([]NewsLinkCandidate, error) {
	event, err := s.store.GetNewsEvent(ctx, eventID)
	if err != nil {
		return nil, err
	}
	candidates, err := s.buildNewsLinkCandidates(ctx, event)
	if err != nil {
		_ = s.store.UpdateNewsEventLinkStatus(ctx, event.ID, NewsEventLinkStatusFailed, time.Now())
		return nil, err
	}
	now := time.Now()
	for i := range candidates {
		if candidates[i].ID == "" {
			candidates[i].ID = generateID()
		}
		if candidates[i].MonitorStatus == "" {
			candidates[i].MonitorStatus = NewsLinkMonitorStatusPending
		}
		if candidates[i].CreatedAt.IsZero() {
			candidates[i].CreatedAt = now
		}
		candidates[i].UpdatedAt = now
	}
	if err := s.store.UpsertNewsLinkCandidates(ctx, candidates); err != nil {
		_ = s.store.UpdateNewsEventLinkStatus(ctx, event.ID, NewsEventLinkStatusFailed, time.Now())
		return nil, err
	}
	status := NewsEventLinkStatusLinked
	if len(candidates) == 0 {
		status = NewsEventLinkStatusNoCandidate
	}
	if err := s.store.UpdateNewsEventLinkStatus(ctx, event.ID, status, time.Now()); err != nil {
		return nil, err
	}
	return candidates, nil
}

func (s *Service) LinkPendingNewsEventsBatch(ctx context.Context, limit int) (LinkNewsEventsBatchResult, error) {
	events, err := s.store.ListPendingNewsEvents(ctx, "", limit)
	if err != nil {
		return LinkNewsEventsBatchResult{}, err
	}
	result := LinkNewsEventsBatchResult{Total: len(events)}
	for _, event := range events {
		candidates, err := s.LinkNewsEvent(ctx, event.ID)
		if err != nil {
			result.Failed++
			result.FailedItems = append(result.FailedItems, UpdateFailure{Symbol: event.ID, Reason: safelog.Text(err.Error(), 240)})
			continue
		}
		result.Candidates += len(candidates)
		if len(candidates) == 0 {
			result.NoCandidate++
		} else {
			result.Linked++
		}
	}
	return result, nil
}

func (s *Service) ListNewsLinkCandidates(ctx context.Context, filter NewsLinkCandidateListFilter) ([]NewsLinkCandidate, error) {
	filter.Limit = normalizedPageLimit(filter.Limit, 500)
	filter.Offset = normalizedPageOffset(filter.Offset)
	return s.store.ListNewsLinkCandidates(ctx, filter)
}

func (s *Service) buildNewsLinkCandidates(ctx context.Context, event NewsEvent) ([]NewsLinkCandidate, error) {
	profiles, err := s.listAllStockProfiles(ctx)
	if err != nil {
		return nil, err
	}
	held, err := s.currentHoldingSymbols(ctx)
	if err != nil {
		return nil, err
	}
	activeStrategy, err := s.activeStrategySymbols(ctx)
	if err != nil {
		return nil, err
	}

	text := normalizeNewsMatchText(event.Title + " " + event.Summary + " " + event.Content)
	accBySymbol := make(map[string]*newsCandidateAccumulator)
	for _, profile := range profiles {
		acc := newNewsCandidateAccumulator(event, profile)
		matchProfileAgainstNews(text, profile, acc)
		if !acc.hasTextEvidence() {
			continue
		}
		accBySymbol[profile.Symbol] = acc
	}
	s.addSemanticNewsProfileCandidates(ctx, event, profiles, accBySymbol)

	items := make([]NewsLinkCandidate, 0, len(accBySymbol))
	for _, acc := range accBySymbol {
		if !acc.hasTextEvidence() {
			continue
		}
		profile := acc.profile
		if _, ok := held[profile.Symbol]; ok {
			acc.addBoost(newsBoostHolding, "当前持仓 boost")
		}
		if _, ok := activeStrategy[profile.Symbol]; ok {
			acc.addBoost(newsBoostActiveStrategy, "活跃策略 boost")
		}
		items = append(items, acc.candidate())
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Score == items[j].Score {
			return items[i].Symbol < items[j].Symbol
		}
		return items[i].Score > items[j].Score
	})
	return items, nil
}

func (s *Service) addSemanticNewsProfileCandidates(ctx context.Context, event NewsEvent, profiles []StockProfile, accBySymbol map[string]*newsCandidateAccumulator) {
	query := strings.TrimSpace(newsEventEmbeddingText(event))
	if query == "" {
		return
	}
	hits, err := s.SemanticSearchStockProfiles(ctx, SemanticSearchRequest{Query: query, Limit: 12})
	if err != nil {
		return
	}
	profileBySymbol := make(map[string]StockProfile, len(profiles))
	for _, profile := range profiles {
		profileBySymbol[profile.Symbol] = profile
	}
	for _, hit := range hits {
		if hit.Score < newsSemanticMinScore {
			continue
		}
		profile := hit.Profile
		if existing, ok := profileBySymbol[profile.Symbol]; ok {
			profile = existing
		}
		if strings.TrimSpace(profile.Symbol) == "" {
			continue
		}
		acc := accBySymbol[profile.Symbol]
		if acc == nil {
			acc = newNewsCandidateAccumulator(event, profile)
			accBySymbol[profile.Symbol] = acc
		}
		score := semanticNewsProfileScore(hit.Score)
		acc.addMatch(NewsLinkMatchSemanticProfile, score, firstNonEmptyOpportunity(profile.Name, profile.Symbol), "语义召回画像 "+firstNonEmptyOpportunity(profile.Name, profile.Symbol))
	}
}

func semanticNewsProfileScore(score float64) float64 {
	if score < 0 {
		score = 0
	}
	if score > 1 {
		score = 1
	}
	return 45 + score*40
}

func (s *Service) listAllStockProfiles(ctx context.Context) ([]StockProfile, error) {
	const pageSize = 500
	out := make([]StockProfile, 0)
	for offset := 0; ; offset += pageSize {
		page, err := s.store.ListStockProfiles(ctx, StockProfileListFilter{Limit: pageSize, Offset: offset})
		if err != nil {
			return nil, err
		}
		if len(page) == 0 {
			break
		}
		out = append(out, page...)
	}
	return out, nil
}

func (s *Service) currentHoldingSymbols(ctx context.Context) (map[string]struct{}, error) {
	out := map[string]struct{}{}
	portfolios, err := s.store.ListPortfolios(ctx)
	if err != nil {
		return nil, err
	}
	for _, portfolio := range portfolios {
		holdings, err := s.store.ListHoldings(ctx, portfolio.ID)
		if err != nil {
			return nil, err
		}
		for _, holding := range holdings {
			if holding.Quantity > 0 {
				out[strings.TrimSpace(holding.Symbol)] = struct{}{}
			}
		}
	}
	return out, nil
}

func (s *Service) activeStrategySymbols(ctx context.Context) (map[string]struct{}, error) {
	out := map[string]struct{}{}
	const pageSize = 200
	for offset := 0; ; offset += pageSize {
		items, err := s.store.ListStrategies(ctx, StrategyListFilter{Status: StrategyStatusActive, Limit: pageSize, Offset: offset})
		if err != nil {
			return nil, err
		}
		if len(items) == 0 {
			break
		}
		for _, item := range items {
			symbol := strings.TrimSpace(item.Strategy.Symbol)
			if symbol != "" {
				out[symbol] = struct{}{}
			}
			if item.Strategy.PortfolioID == "" {
				continue
			}
			holdings, err := s.store.ListHoldings(ctx, item.Strategy.PortfolioID)
			if err != nil {
				return nil, err
			}
			for _, holding := range holdings {
				if holding.Quantity > 0 {
					out[strings.TrimSpace(holding.Symbol)] = struct{}{}
				}
			}
		}
	}
	return out, nil
}

type newsCandidateAccumulator struct {
	event        NewsEvent
	profile      StockProfile
	matchMethod  string
	baseScore    float64
	boostScore   float64
	reasons      []string
	reasonSeen   map[string]struct{}
	matchedTerms []string
	termSeen     map[string]struct{}
}

func newNewsCandidateAccumulator(event NewsEvent, profile StockProfile) *newsCandidateAccumulator {
	return &newsCandidateAccumulator{
		event:      event,
		profile:    profile,
		reasonSeen: map[string]struct{}{},
		termSeen:   map[string]struct{}{},
	}
}

func (a *newsCandidateAccumulator) addMatch(method string, score float64, term string, reason string) {
	if score > a.baseScore {
		a.baseScore = score
		a.matchMethod = method
	}
	a.addTerm(term)
	a.addReason(reason)
}

func (a *newsCandidateAccumulator) addBoost(score float64, reason string) {
	a.boostScore += score
	a.addReason(reason)
}

func (a *newsCandidateAccumulator) hasTextEvidence() bool {
	return a.baseScore > 0
}

func (a *newsCandidateAccumulator) candidate() NewsLinkCandidate {
	score := a.baseScore + a.boostScore
	if score > 120 {
		score = 120
	}
	matchMethod := a.matchMethod
	if a.boostScore > 0 {
		matchMethod = NewsLinkMatchBoosted
	}
	return NewsLinkCandidate{
		NewsEventID:    a.event.ID,
		RawNewsID:      a.event.RawNewsID,
		Symbol:         a.profile.Symbol,
		Market:         a.profile.Market,
		InstrumentName: a.profile.Name,
		MatchMethod:    matchMethod,
		Score:          score,
		Reason:         strings.Join(a.reasons, "；"),
		MatchedTerms:   a.matchedTerms,
	}
}

func (a *newsCandidateAccumulator) addTerm(term string) {
	term = strings.TrimSpace(term)
	if term == "" {
		return
	}
	key := strings.ToLower(term)
	if _, ok := a.termSeen[key]; ok {
		return
	}
	a.termSeen[key] = struct{}{}
	a.matchedTerms = append(a.matchedTerms, term)
}

func (a *newsCandidateAccumulator) addReason(reason string) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return
	}
	if _, ok := a.reasonSeen[reason]; ok {
		return
	}
	a.reasonSeen[reason] = struct{}{}
	a.reasons = append(a.reasons, reason)
}

func matchProfileAgainstNews(text string, profile StockProfile, acc *newsCandidateAccumulator) {
	for _, term := range []string{
		profile.Symbol,
		profile.Market + profile.Symbol,
		profile.Symbol + "." + profile.Market,
	} {
		if newsTermMatched(text, term) {
			acc.addMatch(NewsLinkMatchExactSymbol, newsScoreExactSymbol, term, "命中股票代码 "+term)
		}
	}
	if newsTermMatched(text, profile.Name) {
		acc.addMatch(NewsLinkMatchExactName, newsScoreExactName, profile.Name, "命中标的名称 "+profile.Name)
	}
	for _, term := range profile.Aliases {
		if isNewsGenericTerm(term) || sameNewsTerm(term, profile.Symbol) || sameNewsTerm(term, profile.Name) {
			continue
		}
		if newsTermMatched(text, term) {
			acc.addMatch(NewsLinkMatchAlias, newsScoreAlias, term, "命中别名 "+term)
		}
	}
	keywords := []string{profile.Industry, profile.TrackingIndex, profile.Theme}
	keywords = append(keywords, profile.Sectors...)
	keywords = append(keywords, profile.Concepts...)
	keywords = append(keywords, profile.Tags...)
	for _, term := range cleanProfileTerms(keywords) {
		if isNewsGenericTerm(term) {
			continue
		}
		if newsTermMatched(text, term) {
			acc.addMatch(NewsLinkMatchKeyword, newsScoreKeyword, term, "命中画像关键词 "+term)
		}
	}

	// ponytail: 先复用 profile_text 的空格分词做高召回兜底;后续接 embedding/分词器时替换这里。
	for _, term := range strings.Fields(profile.ProfileText) {
		if !usefulNewsProfileTextTerm(term) || sameNewsTerm(term, profile.Symbol) || sameNewsTerm(term, profile.Name) {
			continue
		}
		if newsTermMatched(text, term) {
			acc.addMatch(NewsLinkMatchProfileKeyword, newsScoreProfileKeyword, term, "命中画像文本 "+term)
		}
	}
}

func normalizeNewsMatchText(text string) string {
	text = strings.ToLower(strings.TrimSpace(text))
	replacer := strings.NewReplacer("，", " ", ",", " ", "。", " ", ".", " ", "；", " ", ";", " ", "：", " ", ":", " ", "、", " ", "\n", " ", "\t", " ")
	return replacer.Replace(text)
}

func newsTermMatched(normalizedText string, term string) bool {
	term = strings.TrimSpace(term)
	if !usefulNewsTerm(term) {
		return false
	}
	return strings.Contains(normalizedText, strings.ToLower(term))
}

func usefulNewsTerm(term string) bool {
	term = strings.TrimSpace(term)
	if term == "" {
		return false
	}
	if isNewsGenericTerm(term) {
		return false
	}
	if len([]rune(term)) < 2 {
		return false
	}
	return true
}

func usefulNewsProfileTextTerm(term string) bool {
	term = strings.TrimSpace(term)
	if !usefulNewsTerm(term) {
		return false
	}
	if isASCIIAlpha(term) && len(term) < newsProfileEnglishMin {
		return false
	}
	return true
}

func isNewsGenericTerm(term string) bool {
	switch strings.ToLower(strings.TrimSpace(term)) {
	case "", "sh", "sz", "bj", "etf", "lof", "股票", "股票标的", "基金", "场内基金", "市场",
		"a", "an", "and", "are", "as", "at", "be", "by", "for", "from", "has", "have",
		"in", "into", "is", "it", "its", "of", "on", "or", "that", "the", "this", "to",
		"was", "were", "will", "with":
		return true
	default:
		return false
	}
}

func isASCIIAlpha(term string) bool {
	if term == "" {
		return false
	}
	for _, r := range term {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') {
			return false
		}
	}
	return true
}

func sameNewsTerm(a, b string) bool {
	return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}
