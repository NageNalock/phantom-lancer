package stockv2

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"phantom-lancer/internal/safelog"
)

func (s *Service) ListStockProfileUpdateTasks(ctx context.Context, filter StockProfileUpdateTaskListFilter) ([]StockProfileUpdateTask, error) {
	filter.Limit = normalizedPageLimit(filter.Limit, 500)
	filter.Offset = normalizedPageOffset(filter.Offset)
	return s.store.ListStockProfileUpdateTasks(ctx, filter)
}

func (s *Service) CountStockProfileUpdateTasks(ctx context.Context, filter StockProfileUpdateTaskListFilter) (int, error) {
	return s.store.CountStockProfileUpdateTasks(ctx, filter)
}

func (s *Service) GetStockProfile(ctx context.Context, symbol string) (StockProfile, error) {
	normalizedSymbol, _ := normalizeQuoteSymbolInput(symbol)
	if normalizedSymbol == "" {
		normalizedSymbol = strings.TrimSpace(symbol)
	}
	return s.store.GetStockProfile(ctx, normalizedSymbol)
}

func (s *Service) updateStockProfileAIState(ctx context.Context, symbol, status, message string, markAttempt bool) error {
	unlock := s.lockStockProfile(symbol)
	defer unlock()
	return s.store.UpdateStockProfileAIState(ctx, symbol, status, message, markAttempt)
}

func (s *Service) ListStockProfiles(ctx context.Context, filter StockProfileListFilter) ([]StockProfile, error) {
	filter.Limit = normalizedPageLimit(filter.Limit, 500)
	filter.Offset = normalizedPageOffset(filter.Offset)
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

func (s *Service) ListStockProfileSummaries(ctx context.Context, symbols []string) (map[string]StockProfileSummary, error) {
	symbols = compactStringList(symbols, 200)
	out, err := s.store.ListStockProfileSummaries(ctx, symbols)
	if err != nil {
		return nil, err
	}
	for _, symbol := range symbols {
		if _, ok := out[symbol]; !ok {
			out[symbol] = StockProfileSummary{Symbol: symbol, Status: "missing", AIProfileStatus: StockProfileAIStatusMissing}
		}
	}
	return out, nil
}

func (s *Service) prepareStockProfileSummaryAgentRun(ctx context.Context, pack StockProfileSummaryContext, requestedBy string) (AgentRun, AgentDecisionLedger, string, error) {
	profile := pack.Profile
	taskProfile, err := s.store.GetAgentTaskProfileByType(ctx, AgentTaskTypeStockProfileSummary)
	if err != nil {
		return AgentRun{}, AgentDecisionLedger{}, "", err
	}
	model, err := s.resolveModel(ctx, taskProfile)
	if err != nil {
		_ = s.updateStockProfileAIState(ctx, profile.Symbol, StockProfileAIStatusNotConfigured, err.Error(), false)
		return AgentRun{}, AgentDecisionLedger{}, "", err
	}
	if s.agentExecutor == nil {
		return AgentRun{}, AgentDecisionLedger{}, "", ErrAgentExecutorUnavailable
	}
	inputArtifact, _ := json.Marshal(map[string]any{
		"task":    AgentTaskTypeStockProfileSummary,
		"context": pack,
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
		return AgentRun{}, AgentDecisionLedger{}, "", err
	}
	return run, ledger, model.ModelName, nil
}

func (s *Service) executeStockProfileAgentRun(ctx context.Context, run AgentRun, ledger AgentDecisionLedger, pack StockProfileSummaryContext, modelName string) {
	profile := pack.Profile
	defer func() {
		if r := recover(); r != nil {
			if s.log != nil {
				s.log.Error("stock profile agent run panicked", "run_id", run.ID, "ledger_id", ledger.ID, "symbol", profile.Symbol, "market", profile.Market, "model", modelName, "panic", r)
			}
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
		s.log.Warn("update stock profile agent run to running failed", "run_id", run.ID, "ledger_id", ledger.ID, "symbol", profile.Symbol, "market", profile.Market, "model", modelName, "error", safelog.Text(err.Error(), 240))
	}
	taskID, _ := s.agentTaskPool.createTask(run.TaskType, run.ID, "", 10*time.Minute)
	execOutput, execErr := s.agentExecutor.ExecuteStockProfileSummary(ctx, taskID, pack, modelName)
	s.finalizeAgentRunWithOutput(ctx, run.ID, ledger.ID, taskID, execOutput, execErr)
}

func (s *Service) applyStockProfileEnhancementResult(ctx context.Context, symbol string, result map[string]any, modelName string, confidence float64) (StockProfile, error) {
	return s.applyStockProfileEnhancementResultForRun(ctx, symbol, "", result, modelName, confidence)
}

func (s *Service) applyStockProfileEnhancementResultForRun(ctx context.Context, symbol, runID string, result map[string]any, modelName string, confidence float64) (StockProfile, error) {
	unlock := s.lockStockProfile(symbol)
	defer unlock()
	if strings.TrimSpace(runID) != "" {
		exists, current, err := s.store.StockProfileAIQueueRunCurrent(ctx, symbol, runID)
		if err != nil {
			return StockProfile{}, err
		}
		if exists && !current {
			return StockProfile{}, ErrStockProfileAIQueueLeaseStale
		}
	}
	profile, err := s.store.GetStockProfile(ctx, strings.TrimSpace(symbol))
	if err != nil {
		return StockProfile{}, err
	}
	if len(result) == 0 {
		_ = s.store.UpdateStockProfileAIState(ctx, profile.Symbol, StockProfileAIStatusFailed, ErrInvalidStockProfileEnhancement.Error(), true)
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
	profile.AIProfileAttemptedAt = profile.AIProfileUpdatedAt
	updated, err := s.store.UpsertStockProfile(ctx, profile)
	if err != nil {
		return StockProfile{}, err
	}
	s.markStockProfileEmbeddingStale(ctx, updated.Symbol)
	return updated, nil
}

// markStockProfileAIEnhancementFailed returns true when the run no longer owns
// the symbol's desired input version. Queue enqueues use the same per-symbol
// lock, so the ownership check and profile state write cannot race each other.
func (s *Service) markStockProfileAIEnhancementFailed(ctx context.Context, run AgentRun, message string) bool {
	if run.TaskType != AgentTaskTypeStockProfileSummary || run.TriggerObjectType != "stock_profile" || strings.TrimSpace(run.TriggerObjectID) == "" {
		return false
	}
	unlock := s.lockStockProfile(run.TriggerObjectID)
	defer unlock()
	exists, current, currentErr := s.store.StockProfileAIQueueRunCurrent(ctx, run.TriggerObjectID, run.ID)
	if currentErr != nil {
		if s.log != nil {
			s.log.Warn("mark stock profile ai failed: validate queue owner failed", "run_id", run.ID, "symbol", run.TriggerObjectID, "error", safelog.Text(currentErr.Error(), 240))
		}
		return false
	}
	if exists && !current {
		return true
	}
	if err := s.store.UpdateStockProfileAIState(ctx, run.TriggerObjectID, StockProfileAIStatusFailed, message, true); err != nil && s.log != nil {
		s.log.Warn("mark stock profile ai failed: save profile failed", "run_id", run.ID, "task_type", run.TaskType, "symbol", run.TriggerObjectID, "error", safelog.Text(err.Error(), 240))
	}
	s.markStockProfileUpdateTaskAIResult(ctx, run.ID, StockProfileUpdateStatusPartial, StockProfileAIStatusFailed, message)
	return false
}

func (s *Service) markStockProfileUpdateTaskAIResult(ctx context.Context, agentRunID, taskStatus, aiStatus, message string) {
	if s.store == nil || strings.TrimSpace(agentRunID) == "" {
		return
	}
	if err := s.store.UpdateStockProfileUpdateTaskAIResultByAgentRunID(ctx, agentRunID, taskStatus, aiStatus, message); err != nil && s.log != nil {
		s.log.Warn("update stock profile task ai result failed", "run_id", agentRunID, "status", aiStatus, "error", err)
	}
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
	unlock := s.lockStockProfile(instrument.Symbol)
	defer unlock()
	profile, err := s.store.UpsertStockProfile(ctx, s.stockProfileFromInstrument(ctx, instrument, false))
	if err != nil {
		return err
	}
	s.markStockProfileEmbeddingStale(ctx, profile.Symbol)
	return nil
}

func (s *Service) markStockProfileEmbeddingStale(ctx context.Context, symbol string) {
	symbol = strings.TrimSpace(symbol)
	if symbol == "" {
		return
	}
	if err := s.store.MarkEmbeddingAssetsStaleForObject(ctx, EmbeddingObjectStockProfile, symbol); err != nil && s.log != nil {
		s.log.Warn("mark stock profile embedding stale failed", "symbol", safelog.Text(symbol, 80), "error", safelog.Text(err.Error(), 240))
	}
}

func (s *Service) stockProfileFromInstrument(ctx context.Context, instrument StockV2Instrument, enrichPublicSources bool) StockProfile {
	profile, _ := s.stockProfileFromInstrumentWithSourceStatuses(ctx, instrument, enrichPublicSources)
	return profile
}

func (s *Service) stockProfileFromInstrumentWithSourceStatuses(ctx context.Context, instrument StockV2Instrument, enrichPublicSources bool) (StockProfile, []StockProfileSourceStatus) {
	base, sourceStatuses := s.stockProfileBaseFromInstrumentWithSourceStatuses(ctx, instrument, enrichPublicSources)
	return s.mergeStockProfileExisting(ctx, base), sourceStatuses
}

func (s *Service) stockProfileBaseFromInstrumentWithSourceStatuses(ctx context.Context, instrument StockV2Instrument, enrichPublicSources bool) (StockProfile, []StockProfileSourceStatus) {
	base := buildStockProfileFromInstrument(instrument)
	var sourceStatuses []StockProfileSourceStatus
	if enrichPublicSources {
		base, sourceStatuses = s.enrichStockProfileFromPublicSources(ctx, base, instrument)
	}
	return base, sourceStatuses
}

func (s *Service) mergeStockProfileExisting(ctx context.Context, base StockProfile) StockProfile {
	existing, err := s.store.GetStockProfile(ctx, base.Symbol)
	if err != nil {
		return base
	}
	// ponytail: profile_update_tasks 保存基础输入 hash;这里仅合并既有 AI 增强与兼容字段。
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
	// 确保 Aliases 包含 AliasesZh / AliasesEn（与 mergeStockProfileAIFields 保持一致）
	profile.Aliases = appendProfileTerms(profile.Aliases, profile.AliasesZh...)
	profile.Aliases = appendProfileTerms(profile.Aliases, profile.AliasesEn...)
	return profile
}

func mergeStockProfileAIFields(base, existing StockProfile) StockProfile {
	base.BaseProfileHash = existing.BaseProfileHash
	base.BaseProfileUpdatedAt = existing.BaseProfileUpdatedAt
	base.BaseProfileCheckedAt = existing.BaseProfileCheckedAt
	base.BusinessSummaryEn = existing.BusinessSummaryEn
	base.BusinessLinesZh = appendProfileTerms(base.BusinessLinesZh, existing.BusinessLinesZh...)
	base.BusinessLinesEn = appendProfileTerms(base.BusinessLinesEn, existing.BusinessLinesEn...)
	base.RiskTagsZh = appendProfileTerms(base.RiskTagsZh, existing.RiskTagsZh...)
	base.RiskTagsEn = appendProfileTerms(base.RiskTagsEn, existing.RiskTagsEn...)
	base.AIProfileStatus = existing.AIProfileStatus
	base.AIProfileModel = existing.AIProfileModel
	base.AIProfileConfidence = existing.AIProfileConfidence
	base.AIProfileError = existing.AIProfileError
	base.AIProfileUpdatedAt = existing.AIProfileUpdatedAt
	base.AIProfileAttemptedAt = existing.AIProfileAttemptedAt
	base.AliasesZh = appendProfileTerms(base.AliasesZh, existing.AliasesZh...)
	base.AliasesEn = appendProfileTerms(base.AliasesEn, existing.AliasesEn...)
	base.KeywordsZh = appendProfileTerms(base.KeywordsZh, existing.KeywordsZh...)
	base.KeywordsEn = appendProfileTerms(base.KeywordsEn, existing.KeywordsEn...)
	if existing.BusinessSummaryZh != "" && (existing.AIProfileStatus == StockProfileAIStatusReady || stockProfileSummaryLooksBasic(base.BusinessSummaryZh)) {
		base.BusinessSummaryZh = existing.BusinessSummaryZh
	}
	// ponytail: BusinessSummary is the freshly fetched base/F10 text. Keep the
	// previous AI summary in BusinessSummaryZh so the next run can compare both
	// inputs without replacing current source data with stale generated text.
	if base.FundType == "" {
		base.FundType = existing.FundType
	}
	if base.TrackingIndex == "" {
		base.TrackingIndex = existing.TrackingIndex
	}
	if base.Theme == "" {
		base.Theme = existing.Theme
	}
	if base.ConstituentHint == "" {
		base.ConstituentHint = existing.ConstituentHint
	}
	base.Aliases = appendProfileTerms(base.Aliases, base.AliasesZh...)
	base.Aliases = appendProfileTerms(base.Aliases, base.AliasesEn...)
	base.ProfileTextZh = buildProfileTextZh(base)
	base.ProfileTextEn = buildProfileTextEn(base)
	base.ProfileText = buildProfileText(base)
	return base
}

func stockProfileSummaryLooksBasic(summary string) bool {
	summary = strings.TrimSpace(summary)
	return summary == "" ||
		strings.Contains(summary, "股票标的") ||
		strings.Contains(summary, "场内基金") ||
		strings.Contains(summary, "行业:") ||
		strings.Contains(summary, "基金类型:")
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

func normalizeStockProfileUpdateTrigger(value string) string {
	switch strings.TrimSpace(value) {
	case StockProfileUpdateTriggerAuto:
		return StockProfileUpdateTriggerAuto
	default:
		return StockProfileUpdateTriggerManual
	}
}

func stockProfileAIDecisionForError(err error) string {
	switch {
	case errors.Is(err, ErrAgentTaskProfileNotFound), errors.Is(err, ErrAgentModelNotAvailable):
		return StockProfileAIDecisionSkippedNotConfigured
	case errors.Is(err, ErrAgentExecutorUnavailable):
		return StockProfileAIDecisionSkippedUnavailable
	default:
		return StockProfileAIDecisionFailed
	}
}

func normalizeStockProfileAIStatus(status string) string {
	switch strings.TrimSpace(status) {
	case StockProfileAIStatusReady:
		return StockProfileAIStatusReady
	case StockProfileAIStatusFailed:
		return StockProfileAIStatusFailed
	case StockProfileAIStatusNotConfigured:
		return StockProfileAIStatusNotConfigured
	case StockProfileAIStatusQueued:
		return StockProfileAIStatusQueued
	case StockProfileAIStatusRunning:
		return StockProfileAIStatusRunning
	default:
		return StockProfileAIStatusMissing
	}
}

func stockProfileFreshnessSummary(profile StockProfile, now time.Time) map[string]any {
	baseCheckedAt := profile.BaseProfileCheckedAt
	if baseCheckedAt.IsZero() {
		baseCheckedAt = profile.BaseProfileUpdatedAt
	}
	baseFresh := !baseCheckedAt.IsZero() &&
		now.Sub(baseCheckedAt) <= baseProfileRefreshInterval+24*time.Hour
	aiReady := normalizeStockProfileAIStatus(profile.AIProfileStatus) == StockProfileAIStatusReady &&
		!profile.AIProfileUpdatedAt.IsZero() &&
		!profile.AIProfileUpdatedAt.Before(profile.BaseProfileUpdatedAt)
	status := "ready"
	if !baseFresh {
		status = "base_stale"
	} else if !aiReady {
		status = "ai_pending"
	}
	return map[string]any{
		"status":               status,
		"profileVersion":       profile.ProfileVersion,
		"baseProfileUpdatedAt": profile.BaseProfileUpdatedAt,
		"baseProfileCheckedAt": baseCheckedAt,
		"aiProfileStatus":      normalizeStockProfileAIStatus(profile.AIProfileStatus),
		"aiProfileUpdatedAt":   profile.AIProfileUpdatedAt,
		"ready":                baseFresh && aiReady,
	}
}

func stockProfileAITaskStatusForDecision(decision string) string {
	switch decision {
	case StockProfileAIDecisionSkippedNotConfigured:
		return StockProfileAIStatusNotConfigured
	case StockProfileAIDecisionSkippedUnavailable, StockProfileAIDecisionFailed:
		return StockProfileAIStatusFailed
	default:
		return StockProfileAIStatusMissing
	}
}

func stockProfileAIInputHash(profile StockProfile) string {
	input := struct {
		Symbol            string   `json:"symbol"`
		Market            string   `json:"market"`
		InstrumentType    string   `json:"instrumentType"`
		Name              string   `json:"name"`
		Aliases           []string `json:"aliases"`
		AliasesZh         []string `json:"aliasesZh"`
		AliasesEn         []string `json:"aliasesEn"`
		Industry          string   `json:"industry"`
		Sectors           []string `json:"sectors"`
		Concepts          []string `json:"concepts"`
		Tags              []string `json:"tags"`
		KeywordsZh        []string `json:"keywordsZh"`
		KeywordsEn        []string `json:"keywordsEn"`
		BusinessSummary   string   `json:"businessSummary"`
		BusinessSummaryZh string   `json:"businessSummaryZh"`
		BusinessLinesZh   []string `json:"businessLinesZh"`
		BusinessLinesEn   []string `json:"businessLinesEn"`
		RiskTagsZh        []string `json:"riskTagsZh"`
		RiskTagsEn        []string `json:"riskTagsEn"`
		FundType          string   `json:"fundType"`
		TrackingIndex     string   `json:"trackingIndex"`
		Theme             string   `json:"theme"`
		ConstituentHint   string   `json:"constituentHint"`
	}{
		Symbol:            profile.Symbol,
		Market:            profile.Market,
		InstrumentType:    profile.InstrumentType,
		Name:              profile.Name,
		Aliases:           cleanProfileTerms(profile.Aliases),
		AliasesZh:         cleanProfileTerms(profile.AliasesZh),
		AliasesEn:         cleanProfileTerms(profile.AliasesEn),
		Industry:          strings.TrimSpace(profile.Industry),
		Sectors:           cleanProfileTerms(profile.Sectors),
		Concepts:          cleanProfileTerms(profile.Concepts),
		Tags:              cleanProfileTerms(profile.Tags),
		KeywordsZh:        cleanProfileTerms(profile.KeywordsZh),
		KeywordsEn:        cleanProfileTerms(profile.KeywordsEn),
		BusinessSummary:   strings.TrimSpace(profile.BusinessSummary),
		BusinessSummaryZh: strings.TrimSpace(profile.BusinessSummaryZh),
		BusinessLinesZh:   cleanProfileTerms(profile.BusinessLinesZh),
		BusinessLinesEn:   cleanProfileTerms(profile.BusinessLinesEn),
		RiskTagsZh:        cleanProfileTerms(profile.RiskTagsZh),
		RiskTagsEn:        cleanProfileTerms(profile.RiskTagsEn),
		FundType:          strings.TrimSpace(profile.FundType),
		TrackingIndex:     strings.TrimSpace(profile.TrackingIndex),
		Theme:             strings.TrimSpace(profile.Theme),
		ConstituentHint:   strings.TrimSpace(profile.ConstituentHint),
	}
	data, _ := json.Marshal(input)
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum[:])
}
