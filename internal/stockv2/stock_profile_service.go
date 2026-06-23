package stockv2

import (
	"context"
	"encoding/json"
	"fmt"
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
	profile := s.stockProfileFromInstrument(ctx, instrument)
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
			if _, err := s.store.UpsertStockProfile(ctx, s.stockProfileFromInstrument(ctx, instrument)); err != nil {
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

func (s *Service) RunAgentStockProfileSummary(ctx context.Context, symbol string, requestedBy string) (AgentRun, error) {
	if s.agentExecutor == nil {
		return AgentRun{}, ErrAgentExecutorUnavailable
	}
	profile, err := s.BuildStockProfile(ctx, symbol)
	if err != nil {
		return AgentRun{}, err
	}
	taskProfile, err := s.store.GetAgentTaskProfileByType(ctx, AgentTaskTypeStockProfileSummary)
	if err != nil {
		return AgentRun{}, err
	}
	model, err := s.resolveModel(ctx, taskProfile)
	if err != nil {
		profile.AIProfileStatus = StockProfileAIStatusNotConfigured
		profile.AIProfileError = err.Error()
		_, _ = s.store.UpsertStockProfile(ctx, profile)
		return AgentRun{}, err
	}
	inputArtifact, _ := json.Marshal(map[string]any{
		"task":    AgentTaskTypeStockProfileSummary,
		"profile": profile,
	})
	run, ledger, err := s.CreateAgentRunRecord(ctx, AgentRunRecordParams{
		TaskType:             AgentTaskTypeStockProfileSummary,
		ProviderID:           model.ProviderID,
		ModelID:              model.ID,
		TriggerObjectType:    "stock_profile",
		TriggerObjectID:      profile.Symbol,
		RequestedBy:          requestedBy,
		InputSummary:         fmt.Sprintf("stock_profile_summary symbol=%s market=%s name=%s", profile.Symbol, profile.Market, profile.Name),
		InputArtifactSummary: string(inputArtifact),
	})
	if err != nil {
		return AgentRun{}, err
	}
	go s.startStockProfileAgentRunAsync(context.Background(), run, ledger, profile, model.ModelName)
	return run, nil
}

func (s *Service) startStockProfileAgentRunAsync(ctx context.Context, run AgentRun, ledger AgentDecisionLedger, profile StockProfile, modelName string) {
	defer func() {
		if r := recover(); r != nil {
			s.finalizeAgentRun(ctx, run.ID, nil, fmt.Errorf("panic: %v", r))
		}
	}()
	if s.agentExecutor == nil {
		s.finalizeAgentRun(ctx, run.ID, nil, fmt.Errorf("no executor configured"))
		return
	}
	running := run
	running.Status = AgentRunStatusRunning
	if _, err := s.store.UpdateAgentRun(ctx, running); err != nil && s.log != nil {
		s.log.Warn("update stock profile agent run to running failed", "run_id", run.ID, "error", err)
	}
	taskID, _ := s.agentTaskPool.createTask(run.TaskType, run.ID, "", 10*time.Minute)
	execOutput, execErr := s.agentExecutor.ExecuteStockProfileSummary(ctx, taskID, profile, modelName)
	s.finalizeAgentRunWithOutput(ctx, run.ID, ledger.ID, taskID, execOutput, execErr)
}

func (s *Service) applyStockProfileEnhancementResult(ctx context.Context, symbol string, result map[string]any, modelName string, confidence float64) (StockProfile, error) {
	profile, err := s.store.GetStockProfile(ctx, strings.TrimSpace(symbol))
	if err != nil {
		return StockProfile{}, err
	}
	if len(result) == 0 {
		profile.AIProfileStatus = StockProfileAIStatusFailed
		profile.AIProfileError = ErrInvalidStockProfileEnhancement.Error()
		_, _ = s.store.UpsertStockProfile(ctx, profile)
		return StockProfile{}, ErrInvalidStockProfileEnhancement
	}
	profile.BusinessSummaryZh = firstProfileResultString(result, "summaryZh", "businessSummaryZh")
	if profile.BusinessSummaryZh == "" {
		profile.BusinessSummaryZh = profile.BusinessSummary
	}
	profile.BusinessSummaryEn = firstProfileResultString(result, "summaryEn", "businessSummaryEn")
	profile.AliasesZh = appendProfileTerms(profile.AliasesZh, profileResultStrings(result, "aliasesZh", "aliasZh")...)
	profile.AliasesEn = appendProfileTerms(profile.AliasesEn, profileResultStrings(result, "aliasesEn", "aliasEn")...)
	profile.KeywordsZh = appendProfileTerms(profile.KeywordsZh, profileResultStrings(result, "keywordsZh", "keywordZh")...)
	profile.KeywordsEn = appendProfileTerms(profile.KeywordsEn, profileResultStrings(result, "keywordsEn", "keywordEn")...)
	profile.BusinessLinesZh = appendProfileTerms(profile.BusinessLinesZh, profileResultStrings(result, "businessLinesZh", "businessLineZh")...)
	profile.BusinessLinesEn = appendProfileTerms(profile.BusinessLinesEn, profileResultStrings(result, "businessLinesEn", "businessLineEn")...)
	profile.RiskTagsZh = appendProfileTerms(profile.RiskTagsZh, profileResultStrings(result, "riskTagsZh", "riskTagZh")...)
	profile.RiskTagsEn = appendProfileTerms(profile.RiskTagsEn, profileResultStrings(result, "riskTagsEn", "riskTagEn")...)
	profile.Aliases = appendProfileTerms(profile.Aliases, profile.AliasesZh...)
	profile.Aliases = appendProfileTerms(profile.Aliases, profile.AliasesEn...)
	profile.ProfileTextZh = buildProfileTextZh(profile)
	profile.ProfileTextEn = buildProfileTextEn(profile)
	profile.ProfileText = buildProfileText(profile)
	profile.AIProfileStatus = StockProfileAIStatusReady
	profile.AIProfileModel = modelName
	profile.AIProfileConfidence = confidence
	profile.AIProfileError = ""
	profile.AIProfileUpdatedAt = time.Now()
	return s.store.UpsertStockProfile(ctx, profile)
}

func firstProfileResultString(result map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(stringFromAny(result[key])); value != "" {
			return value
		}
	}
	return ""
}

func profileResultStrings(result map[string]any, keys ...string) []string {
	for _, key := range keys {
		if items := stringsFromAny(result[key]); len(items) > 0 {
			return items
		}
	}
	return nil
}

func stringsFromAny(value any) []string {
	switch typed := value.(type) {
	case []string:
		return cleanProfileTerms(typed)
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if text := strings.TrimSpace(stringFromAny(item)); text != "" {
				out = append(out, text)
			}
		}
		return cleanProfileTerms(out)
	case string:
		if strings.TrimSpace(typed) == "" {
			return nil
		}
		return cleanProfileTerms(strings.FieldsFunc(typed, func(r rune) bool {
			return r == ',' || r == '，' || r == ';' || r == '；' || r == '、' || r == '\n'
		}))
	default:
		return nil
	}
}

func (s *Service) upsertInstrumentWithProfile(ctx context.Context, instrument StockV2Instrument) error {
	if err := s.store.UpsertInstrument(ctx, instrument); err != nil {
		return err
	}
	_, err := s.store.UpsertStockProfile(ctx, s.stockProfileFromInstrument(ctx, instrument))
	return err
}

func (s *Service) stockProfileFromInstrument(ctx context.Context, instrument StockV2Instrument) StockProfile {
	base := buildStockProfileFromInstrument(instrument)
	existing, err := s.store.GetStockProfile(ctx, base.Symbol)
	if err != nil {
		return base
	}
	// ponytail: 没有 profile_text_hash 前先保留已有 AI 增强;后续有真实变更检测再置 stale。
	return mergeStockProfileAIFields(base, existing)
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
		Symbol:          strings.TrimSpace(instrument.Symbol),
		Market:          strings.TrimSpace(instrument.Market),
		InstrumentType:  instrument.InstrumentType,
		Name:            strings.TrimSpace(instrument.Name),
		Aliases:         aliases,
		AliasesZh:       cleanProfileTerms([]string{strings.TrimSpace(instrument.Name)}),
		AliasesEn:       cleanProfileTerms([]string{instrument.Symbol, instrument.Market + instrument.Symbol, instrument.Symbol + "." + instrument.Market}),
		Industry:        strings.TrimSpace(instrument.Industry),
		Sectors:         sectors,
		Concepts:        concepts,
		Tags:            tags,
		KeywordsZh:      cleanProfileTerms(append([]string{instrument.Industry, instrument.Sector}, instrument.Concepts...)),
		KeywordsEn:      cleanProfileTerms([]string{instrument.Symbol, instrument.Market}),
		ProfileVersion:  2,
		AIProfileStatus: StockProfileAIStatusMissing,
	}
	if profile.Name == "" {
		profile.Name = profile.Symbol
	}
	if profile.InstrumentType == InstrumentTypeExchangeFund || looksLikeExchangeFund(profile.Name) {
		enrichExchangeFundProfile(&profile)
	}
	profile.BusinessSummary = buildProfileSummary(profile)
	profile.BusinessSummaryZh = profile.BusinessSummary
	profile.ProfileTextZh = buildProfileTextZh(profile)
	profile.ProfileTextEn = buildProfileTextEn(profile)
	profile.ProfileText = buildProfileText(profile)
	return profile
}

func mergeStockProfileAIFields(base, existing StockProfile) StockProfile {
	base.BusinessSummaryEn = existing.BusinessSummaryEn
	base.BusinessLinesZh = existing.BusinessLinesZh
	base.BusinessLinesEn = existing.BusinessLinesEn
	base.RiskTagsZh = existing.RiskTagsZh
	base.RiskTagsEn = existing.RiskTagsEn
	base.AIProfileStatus = existing.AIProfileStatus
	base.AIProfileModel = existing.AIProfileModel
	base.AIProfileConfidence = existing.AIProfileConfidence
	base.AIProfileError = existing.AIProfileError
	base.AIProfileUpdatedAt = existing.AIProfileUpdatedAt
	base.AliasesZh = appendProfileTerms(base.AliasesZh, existing.AliasesZh...)
	base.AliasesEn = appendProfileTerms(base.AliasesEn, existing.AliasesEn...)
	base.KeywordsZh = appendProfileTerms(base.KeywordsZh, existing.KeywordsZh...)
	base.KeywordsEn = appendProfileTerms(base.KeywordsEn, existing.KeywordsEn...)
	if existing.BusinessSummaryZh != "" {
		base.BusinessSummaryZh = existing.BusinessSummaryZh
	}
	base.Aliases = appendProfileTerms(base.Aliases, base.AliasesZh...)
	base.Aliases = appendProfileTerms(base.Aliases, base.AliasesEn...)
	base.ProfileTextZh = buildProfileTextZh(base)
	base.ProfileTextEn = buildProfileTextEn(base)
	base.ProfileText = buildProfileText(base)
	return base
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
		themeName := strings.TrimSpace(strings.ReplaceAll(name, "ETF", ""))
		profile.Tags = appendProfileTerms(profile.Tags, "ETF", themeName)
		profile.AliasesEn = appendProfileTerms(profile.AliasesEn, "ETF")
		profile.KeywordsEn = appendProfileTerms(profile.KeywordsEn, "ETF", themeName)
	}
	if strings.Contains(upperName, "LOF") {
		profile.FundType = "LOF"
		profile.Tags = appendProfileTerms(profile.Tags, "LOF")
		profile.AliasesEn = appendProfileTerms(profile.AliasesEn, "LOF")
		profile.KeywordsEn = appendProfileTerms(profile.KeywordsEn, "LOF")
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
	profile.AliasesZh = appendProfileTerms(profile.AliasesZh, profile.FundType, profile.TrackingIndex, profile.Theme)
	profile.KeywordsZh = appendProfileTerms(profile.KeywordsZh, profile.FundType, profile.TrackingIndex, profile.Theme, profile.ConstituentHint)
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
	terms := []string{profile.ProfileTextZh, profile.ProfileTextEn}
	terms = append(terms, profile.Aliases...)
	terms = append(terms, profile.AliasesZh...)
	terms = append(terms, profile.AliasesEn...)
	terms = append(terms, profile.KeywordsZh...)
	terms = append(terms, profile.KeywordsEn...)
	terms = append(terms, profile.BusinessLinesZh...)
	terms = append(terms, profile.BusinessLinesEn...)
	terms = append(terms, profile.RiskTagsZh...)
	terms = append(terms, profile.RiskTagsEn...)
	return strings.Join(cleanProfileTerms(terms), " ")
}

func buildProfileTextZh(profile StockProfile) string {
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
	terms = append(terms, profile.AliasesZh...)
	terms = append(terms, profile.KeywordsZh...)
	terms = append(terms, profile.BusinessLinesZh...)
	terms = append(terms, profile.RiskTagsZh...)
	return strings.Join(cleanProfileTerms(terms), " ")
}

func buildProfileTextEn(profile StockProfile) string {
	terms := []string{
		profile.Symbol,
		profile.Market,
		profile.Market + profile.Symbol,
		profile.Symbol + "." + profile.Market,
		profile.BusinessSummaryEn,
		profile.FundType,
	}
	terms = append(terms, profile.AliasesEn...)
	terms = append(terms, profile.KeywordsEn...)
	terms = append(terms, profile.BusinessLinesEn...)
	terms = append(terms, profile.RiskTagsEn...)
	return strings.Join(cleanProfileTerms(terms), " ")
}
