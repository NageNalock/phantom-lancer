package stockv2

import (
	"context"
	"strings"
	"time"
)

func (s *Service) BuildStockProfile(ctx context.Context, symbol string) (StockProfile, error) {
	normalizedSymbol, _ := normalizeQuoteSymbolInput(symbol)
	if normalizedSymbol == "" {
		normalizedSymbol = strings.TrimSpace(symbol)
	}
	instrument, err := s.store.GetInstrument(ctx, normalizedSymbol)
	if err != nil {
		return StockProfile{}, err
	}
	profile := buildStockProfileFromInstrument(instrument)
	return s.store.UpsertStockProfile(ctx, profile)
}

func (s *Service) RebuildStockProfiles(ctx context.Context) (RebuildStockProfilesResult, error) {
	total, err := s.store.CountInstruments(ctx)
	if err != nil {
		return RebuildStockProfilesResult{}, err
	}
	result := RebuildStockProfilesResult{Total: total, UpdatedAt: time.Now()}
	const pageSize = 500
	for offset := 0; ; offset += pageSize {
		instruments, err := s.store.GetInstruments(ctx, pageSize, offset)
		if err != nil {
			return result, err
		}
		if len(instruments) == 0 {
			break
		}
		for _, instrument := range instruments {
			if _, err := s.store.UpsertStockProfile(ctx, buildStockProfileFromInstrument(instrument)); err != nil {
				result.Failed++
				result.FailedItems = append(result.FailedItems, UpdateFailure{
					Symbol: instrument.Symbol,
					Reason: err.Error(),
				})
				continue
			}
			result.Success++
		}
	}
	return result, nil
}

func (s *Service) GetStockProfile(ctx context.Context, symbol string) (StockProfile, error) {
	normalizedSymbol, _ := normalizeQuoteSymbolInput(symbol)
	if normalizedSymbol == "" {
		normalizedSymbol = strings.TrimSpace(symbol)
	}
	return s.store.GetStockProfile(ctx, normalizedSymbol)
}

func (s *Service) ListStockProfiles(ctx context.Context, filter StockProfileListFilter) ([]StockProfile, error) {
	filter.Limit = normalizedStockProfileLimit(filter.Limit)
	filter.Offset = normalizedStockProfileOffset(filter.Offset)
	if filter.InstrumentType != "" {
		filter.InstrumentType = normalizeInstrumentType(filter.InstrumentType)
	}
	return s.store.ListStockProfiles(ctx, filter)
}

func (s *Service) CountStockProfiles(ctx context.Context, filter StockProfileListFilter) (int, error) {
	if filter.InstrumentType != "" {
		filter.InstrumentType = normalizeInstrumentType(filter.InstrumentType)
	}
	return s.store.CountStockProfiles(ctx, filter)
}

func buildStockProfileFromInstrument(instrument StockV2Instrument) StockProfile {
	instrument.InstrumentType = normalizeInstrumentType(instrument.InstrumentType)
	aliases := cleanProfileTerms([]string{
		instrument.Symbol,
		instrument.Market + instrument.Symbol,
		instrument.Symbol + "." + instrument.Market,
		instrument.Name,
	})
	sectors := cleanProfileTerms([]string{instrument.Sector})
	concepts := cleanProfileTerms(instrument.Concepts)
	tags := cleanProfileTerms([]string{instrument.Industry, instrument.Sector})

	profile := StockProfile{
		Symbol:         strings.TrimSpace(instrument.Symbol),
		Market:         strings.TrimSpace(instrument.Market),
		InstrumentType: instrument.InstrumentType,
		Name:           strings.TrimSpace(instrument.Name),
		Aliases:        aliases,
		Industry:       strings.TrimSpace(instrument.Industry),
		Sectors:        sectors,
		Concepts:       concepts,
		Tags:           tags,
		ProfileVersion: 1,
	}
	if profile.Name == "" {
		profile.Name = profile.Symbol
	}
	if profile.InstrumentType == InstrumentTypeExchangeFund || looksLikeExchangeFund(profile.Name) {
		enrichExchangeFundProfile(&profile)
	}
	profile.BusinessSummary = buildProfileSummary(profile)
	profile.ProfileText = buildProfileText(profile)
	return profile
}

func cleanProfileTerms(items []string) []string {
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
		term := strings.TrimSpace(item)
		term = strings.Trim(term, "，,;；、 ")
		if term == "" || term == "." {
			continue
		}
		key := strings.ToLower(term)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, term)
	}
	return out
}

func appendProfileTerms(base []string, items ...string) []string {
	return cleanProfileTerms(append(base, items...))
}

func looksLikeExchangeFund(name string) bool {
	upperName := strings.ToUpper(name)
	return strings.Contains(upperName, "ETF") ||
		strings.Contains(upperName, "LOF") ||
		strings.Contains(name, "基金")
}

func enrichExchangeFundProfile(profile *StockProfile) {
	name := profile.Name
	upperName := strings.ToUpper(name)
	profile.FundType = "场内基金"
	profile.Tags = appendProfileTerms(profile.Tags, "场内基金")
	if strings.Contains(upperName, "ETF") {
		profile.FundType = "ETF"
		profile.Tags = appendProfileTerms(profile.Tags, "ETF", strings.TrimSpace(strings.ReplaceAll(name, "ETF", "")))
	}
	if strings.Contains(upperName, "LOF") {
		profile.FundType = "LOF"
		profile.Tags = appendProfileTerms(profile.Tags, "LOF")
	}

	// ponytail: ETF 画像先用名称关键词做高召回种子;等有正式基金画像源后替换这里的规则表。
	for _, rule := range []struct {
		keyword string
		index   string
		theme   string
	}{
		{"沪深300", "沪深300", "宽基指数"},
		{"中证500", "中证500", "宽基指数"},
		{"中证1000", "中证1000", "宽基指数"},
		{"上证50", "上证50", "宽基指数"},
		{"科创50", "科创50", "科创板"},
		{"创业板", "创业板", "创业板"},
		{"红利", "", "红利"},
		{"证券", "", "证券"},
		{"银行", "", "银行"},
		{"医药", "", "医药"},
		{"新能源", "", "新能源"},
		{"人工智能", "", "人工智能"},
		{"芯片", "", "芯片"},
		{"半导体", "", "半导体"},
		{"军工", "", "军工"},
	} {
		if !strings.Contains(name, rule.keyword) {
			continue
		}
		profile.Tags = appendProfileTerms(profile.Tags, rule.keyword, rule.theme)
		if profile.TrackingIndex == "" && rule.index != "" {
			profile.TrackingIndex = rule.index
		}
		if profile.Theme == "" && rule.theme != "" {
			profile.Theme = rule.theme
		}
	}
	if profile.TrackingIndex != "" {
		profile.ConstituentHint = "跟踪 " + profile.TrackingIndex + " 相关成分股"
	} else if profile.Theme != "" {
		profile.ConstituentHint = "关注 " + profile.Theme + " 主题相关成分股"
	}
	profile.Aliases = appendProfileTerms(profile.Aliases, profile.FundType, profile.TrackingIndex, profile.Theme)
}

func buildProfileSummary(profile StockProfile) string {
	parts := []string{profile.Name + "(" + profile.Symbol + ")"}
	if profile.Market != "" {
		parts = append(parts, profile.Market+"市场")
	}
	if profile.InstrumentType == InstrumentTypeExchangeFund {
		parts = append(parts, "场内基金")
	} else {
		parts = append(parts, "股票标的")
	}
	if profile.Industry != "" {
		parts = append(parts, "行业:"+profile.Industry)
	}
	if len(profile.Sectors) > 0 {
		parts = append(parts, "板块:"+strings.Join(profile.Sectors, "、"))
	}
	if len(profile.Concepts) > 0 {
		parts = append(parts, "概念:"+strings.Join(profile.Concepts, "、"))
	}
	if profile.FundType != "" {
		parts = append(parts, "基金类型:"+profile.FundType)
	}
	if profile.TrackingIndex != "" {
		parts = append(parts, "跟踪指数:"+profile.TrackingIndex)
	}
	if profile.Theme != "" {
		parts = append(parts, "主题:"+profile.Theme)
	}
	return strings.Join(parts, "。")
}

func buildProfileText(profile StockProfile) string {
	terms := []string{
		profile.Symbol,
		profile.Market,
		profile.Market + profile.Symbol,
		profile.Symbol + "." + profile.Market,
		profile.Name,
		profile.Industry,
		profile.BusinessSummary,
		profile.FundType,
		profile.TrackingIndex,
		profile.Theme,
		profile.ConstituentHint,
	}
	terms = append(terms, profile.Aliases...)
	terms = append(terms, profile.Sectors...)
	terms = append(terms, profile.Concepts...)
	terms = append(terms, profile.Tags...)
	return strings.Join(cleanProfileTerms(terms), " ")
}
