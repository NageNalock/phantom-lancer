package stockv2

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBuildStockProfileForStock(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newStockProfileTestService(t)
	defer cleanup()

	if err := svc.store.UpsertInstrument(ctx, StockV2Instrument{
		ID:             "inst-300750",
		Symbol:         "300750",
		Market:         "SZ",
		InstrumentType: InstrumentTypeStock,
		Name:           "宁德时代",
		Industry:       "电力设备",
		Sector:         "新能源",
		Concepts:       []string{"锂电池", "储能"},
		Status:         "active",
	}); err != nil {
		t.Fatalf("upsert instrument: %v", err)
	}

	profile, err := svc.BuildStockProfile(ctx, "300750")
	if err != nil {
		t.Fatalf("build profile: %v", err)
	}
	if profile.Symbol != "300750" || profile.Name != "宁德时代" || profile.Industry != "电力设备" {
		t.Fatalf("profile = %+v, want stock identity fields", profile)
	}
	for _, keyword := range []string{"宁德时代", "锂电池", "储能", "SZ300750"} {
		if !strings.Contains(profile.ProfileText, keyword) {
			t.Fatalf("profile text %q does not contain %q", profile.ProfileText, keyword)
		}
	}
	if profile.AIProfileStatus != StockProfileAIStatusMissing {
		t.Fatalf("ai status = %q, want missing", profile.AIProfileStatus)
	}
	if !profileContainsString(profile.AliasesZh, "宁德时代") || !profileContainsString(profile.AliasesEn, "300750.SZ") {
		t.Fatalf("bilingual aliases missing: zh=%v en=%v", profile.AliasesZh, profile.AliasesEn)
	}
}

func TestBuildStockProfileForExchangeFund(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newStockProfileTestService(t)
	defer cleanup()

	if err := svc.store.UpsertInstrument(ctx, StockV2Instrument{
		ID:             "inst-510300",
		Symbol:         "510300",
		Market:         "SH",
		InstrumentType: InstrumentTypeExchangeFund,
		Name:           "沪深300ETF",
		Status:         "active",
	}); err != nil {
		t.Fatalf("upsert instrument: %v", err)
	}

	profile, err := svc.BuildStockProfile(ctx, "sh510300")
	if err != nil {
		t.Fatalf("build profile: %v", err)
	}
	if profile.FundType != "ETF" || profile.TrackingIndex != "沪深300" || profile.Theme != "宽基指数" {
		t.Fatalf("fund profile = %+v, want ETF fields", profile)
	}
	for _, keyword := range []string{"场内基金", "ETF", "沪深300", "宽基指数"} {
		if !profileContainsString(profile.Tags, keyword) && !strings.Contains(profile.ProfileText, keyword) {
			t.Fatalf("ETF profile missing keyword %q: %+v", keyword, profile)
		}
	}
}

func TestBuildStockProfileEnrichesStockFromPublicF10(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newStockProfileTestServiceWithClient(t, &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/PC_HSF10/CompanySurvey/CompanySurveyAjax":
			return stringResponse(http.StatusOK, `{"jbzl":{"gsmc":"宁德时代新能源科技股份有限公司","agjc":"宁德时代","ywmc":"Contemporary Amperex Technology Co., Limited","sshy":"电池","sszjhhy":"电气机械和器材制造业","gsjj":"公司是全球领先的新能源创新科技公司,主要从事动力电池和储能电池系统研发、生产和销售。","jyfw":"锂离子电池、动力电池系统、储能电池系统、电池材料及回收业务"}}`), nil
		case "/PC_HSF10/BusinessAnalysis/PageAjax":
			return stringResponse(http.StatusOK, `{"zygcfx":[{"REPORT_DATE":"2025-12-31","MAINOP_TYPE":"2","ITEM_NAME":"动力电池系统","MBI_RATIO":"72.10"},{"REPORT_DATE":"2025-12-31","MAINOP_TYPE":"2","ITEM_NAME":"储能电池系统","MBI_RATIO":"18.20"},{"REPORT_DATE":"2024-12-31","MAINOP_TYPE":"2","ITEM_NAME":"旧业务","MBI_RATIO":"99.00"}]}`), nil
		case "/PC_HSF10/CoreConception/PageAjax":
			return stringResponse(http.StatusOK, `{"ssbk":[{"BOARD_NAME":"储能概念"},{"BOARD_NAME":"融资融券"}],"hxtc":[{"KEYWORD":"动力电池","KEY_CLASSIF":"主营业务"},{"KEYWORD":"新能源车渗透率","KEY_CLASSIF":"行业背景"}]}`), nil
		default:
			return stringResponse(http.StatusNotFound, "not found"), nil
		}
	})})
	defer cleanup()

	if err := svc.store.UpsertInstrument(ctx, StockV2Instrument{
		ID:             "inst-300750",
		Symbol:         "300750",
		Market:         "SZ",
		InstrumentType: InstrumentTypeStock,
		Name:           "宁德时代",
		Status:         "active",
	}); err != nil {
		t.Fatalf("upsert instrument: %v", err)
	}

	profile, err := svc.BuildStockProfile(ctx, "300750")
	if err != nil {
		t.Fatalf("build profile: %v", err)
	}
	for _, want := range []string{"全球领先", "动力电池系统", "储能电池系统", "储能概念", "新能源车渗透率"} {
		if !strings.Contains(profile.ProfileText, want) {
			t.Fatalf("profile text %q missing %q; profile=%+v", profile.ProfileText, want, profile)
		}
	}
	if profileContainsString(profile.Concepts, "融资融券") {
		t.Fatalf("concepts = %#v, want noisy F10 concept filtered", profile.Concepts)
	}
}

func TestBuildStockProfileEnrichesFundFromPublicF10(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newStockProfileTestServiceWithClient(t, &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/jbgk_169201.html":
			return stringResponse(http.StatusOK, `<html><body><table>
<tr><th>基金全称</th><td>浙商鼎盈事件驱动混合型证券投资基金(LOF)</td></tr>
<tr><th>基金类型</th><td>混合型-灵活</td></tr>
<tr><th>业绩比较基准</th><td>沪深300指数收益率*50%+中证综合债指数收益率*50%</td></tr>
</table>
<label class="left">投资目标</label><p>精选事件驱动主题相关证券,力争实现基金资产长期稳健增值。</p>
<label class="left">投资范围</label><p>投资于股票、债券、货币市场工具、资产支持证券等。</p>
<label class="left">风险收益特征</label><p>本基金属于混合型基金,风险收益高于债券型基金。</p></body></html>`), nil
		case "/FundArchivesDatas.aspx":
			return stringResponse(http.StatusOK, `var apidata={content:"<table><tr><td>1</td><td><a>688012</a></td><td class='tol'><a>中微公司</a></td><td>100</td></tr><tr><td>2</td><td><a>688536</a></td><td class='tol'><a>思瑞浦</a></td><td>50</td></tr></table>"};`), nil
		default:
			return stringResponse(http.StatusNotFound, "not found"), nil
		}
	})})
	defer cleanup()

	if err := svc.store.UpsertInstrument(ctx, StockV2Instrument{
		ID:             "inst-169201",
		Symbol:         "169201",
		Market:         "SZ",
		InstrumentType: InstrumentTypeExchangeFund,
		Name:           "浙商鼎盈LOF",
		Status:         "active",
	}); err != nil {
		t.Fatalf("upsert instrument: %v", err)
	}

	profile, err := svc.BuildStockProfile(ctx, "169201")
	if err != nil {
		t.Fatalf("build profile: %v", err)
	}
	for _, want := range []string{"混合型-灵活", "事件驱动", "中微公司", "思瑞浦", "前十大持仓"} {
		if !strings.Contains(profile.ProfileText, want) && !strings.Contains(profile.ConstituentHint, want) {
			t.Fatalf("profile missing %q: %+v", want, profile)
		}
	}
}

func TestRebuildStockProfilesDoesNotCallPublicF10(t *testing.T) {
	ctx := context.Background()
	sourceCalls := 0
	svc, cleanup := newStockProfileTestServiceWithClient(t, &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		sourceCalls++
		return stringResponse(http.StatusOK, "{}"), nil
	})})
	defer cleanup()

	if err := svc.store.UpsertInstrument(ctx, StockV2Instrument{
		ID:             "inst-300750",
		Symbol:         "300750",
		Market:         "SZ",
		InstrumentType: InstrumentTypeStock,
		Name:           "宁德时代",
		Status:         "active",
	}); err != nil {
		t.Fatalf("upsert instrument: %v", err)
	}

	if _, err := svc.RebuildStockProfiles(ctx); err != nil {
		t.Fatalf("rebuild profiles: %v", err)
	}
	if sourceCalls != 0 {
		t.Fatalf("rebuild made %d public source calls, want 0", sourceCalls)
	}
}

func TestUpdateStockProfileSkipsAIWhenInputUnchanged(t *testing.T) {
	ctx := context.Background()
	businessLine := "动力电池系统"
	svc, cleanup := newStockProfileTestServiceWithClient(t, stockProfileF10TestClient(&businessLine))
	defer cleanup()
	seedProfileInstrument(t, svc, ctx)
	configureStockProfileAgent(t, svc, ctx)
	svc.agentExecutor = fakeOperationReviewExecutor{
		pool:       svc.agentTaskPool,
		submit:     true,
		summary:    "profile enhanced",
		confidence: 0.8,
		result: map[string]any{
			"summaryZh":  "AI 增强摘要",
			"keywordsZh": []any{"动力电池"},
		},
	}

	first, err := svc.UpdateStockProfile(ctx, RequestUpdateStockProfile{Symbol: "300750", TriggerSource: StockProfileUpdateTriggerManual, RequestedBy: "test"})
	if err != nil {
		t.Fatalf("first update profile: %v", err)
	}
	if !first.Task.BaseInputChanged || first.Task.AIDecision != StockProfileAIDecisionCalled || first.AgentRun == nil {
		t.Fatalf("first result = %+v, want changed and ai called", first)
	}
	if first.Task.Status != StockProfileUpdateStatusRunning || first.Task.BaseProfileStatus != StockProfileUpdateBaseStatusReady || first.Task.AIProfileStatus != StockProfileUpdateAIStatusRunning {
		t.Fatalf("first task status = %+v, want base ready and ai running", first.Task)
	}
	_ = waitAgentRunTerminal(t, svc, first.AgentRun.ID)
	firstTasks, err := svc.ListStockProfileUpdateTasks(ctx, StockProfileUpdateTaskListFilter{Symbol: "300750", Limit: 1})
	if err != nil {
		t.Fatalf("list first profile update task: %v", err)
	}
	if len(firstTasks) != 1 || firstTasks[0].Status != StockProfileUpdateStatusCompleted || firstTasks[0].AIProfileStatus != StockProfileAIStatusReady {
		t.Fatalf("first persisted task = %+v, want completed with ai ready", firstTasks)
	}

	second, err := svc.UpdateStockProfile(ctx, RequestUpdateStockProfile{Symbol: "300750", TriggerSource: StockProfileUpdateTriggerManual, RequestedBy: "test"})
	if err != nil {
		t.Fatalf("second update profile: %v", err)
	}
	if second.Task.BaseInputChanged || second.Task.AIDecision != StockProfileAIDecisionSkippedUnchanged || second.AgentRun != nil {
		t.Fatalf("second result = %+v, want unchanged and ai skipped", second)
	}
	if second.Task.BaseProfileStatus != StockProfileUpdateBaseStatusReady || second.Task.AIProfileStatus != StockProfileAIStatusReady {
		t.Fatalf("second task profile status = %+v, want base ready and ai ready", second.Task)
	}
	tasks, err := svc.ListStockProfileUpdateTasks(ctx, StockProfileUpdateTaskListFilter{Symbol: "300750", Limit: 10})
	if err != nil {
		t.Fatalf("list profile update tasks: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("tasks len = %d, want 2", len(tasks))
	}
}

func TestUpdateStockProfileCallsAIWhenInputChanges(t *testing.T) {
	ctx := context.Background()
	businessLine := "动力电池系统"
	svc, cleanup := newStockProfileTestServiceWithClient(t, stockProfileF10TestClient(&businessLine))
	defer cleanup()
	seedProfileInstrument(t, svc, ctx)
	configureStockProfileAgent(t, svc, ctx)
	svc.agentExecutor = fakeOperationReviewExecutor{
		pool:       svc.agentTaskPool,
		submit:     true,
		summary:    "profile enhanced",
		confidence: 0.8,
		result: map[string]any{
			"summaryZh":  "AI 增强摘要",
			"keywordsZh": []any{"动力电池"},
		},
	}

	first, err := svc.UpdateStockProfile(ctx, RequestUpdateStockProfile{Symbol: "300750", TriggerSource: StockProfileUpdateTriggerManual, RequestedBy: "test"})
	if err != nil {
		t.Fatalf("first update profile: %v", err)
	}
	if first.AgentRun == nil {
		t.Fatalf("first result = %+v, want ai run", first)
	}
	_ = waitAgentRunTerminal(t, svc, first.AgentRun.ID)

	businessLine = "麒麟电池系统"
	second, err := svc.UpdateStockProfile(ctx, RequestUpdateStockProfile{Symbol: "300750", TriggerSource: StockProfileUpdateTriggerAuto, RequestedBy: "test"})
	if err != nil {
		t.Fatalf("second update profile: %v", err)
	}
	if !second.Task.BaseInputChanged || second.Task.AIDecision != StockProfileAIDecisionCalled || second.AgentRun == nil {
		t.Fatalf("second result = %+v, want changed and ai called", second)
	}
}

func TestStockProfileDedupesAliasesAndTags(t *testing.T) {
	profile := buildStockProfileFromInstrument(StockV2Instrument{
		Symbol:         "000001",
		Market:         "SZ",
		InstrumentType: InstrumentTypeStock,
		Name:           "平安银行",
		Industry:       "银行",
		Sector:         " 银行 ",
		Concepts:       []string{"金融", "金融", " 银行 "},
		Status:         "active",
	})
	if countProfileString(profile.Tags, "银行") != 1 {
		t.Fatalf("tags = %#v, want 银行 once", profile.Tags)
	}
	if countProfileString(profile.Concepts, "金融") != 1 {
		t.Fatalf("concepts = %#v, want 金融 once", profile.Concepts)
	}
	if countProfileString(profile.Aliases, "平安银行") != 1 {
		t.Fatalf("aliases = %#v, want 平安银行 once", profile.Aliases)
	}
}

func TestUpsertInstrumentWithProfileMaintainsProfile(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newStockProfileTestService(t)
	defer cleanup()

	inst := StockV2Instrument{
		ID:             "inst-301321",
		Symbol:         "301321",
		Market:         "SZ",
		InstrumentType: InstrumentTypeStock,
		Name:           "翰博高新",
		Industry:       "光学光电子",
		Sector:         "消费电子",
		Status:         "active",
	}
	if err := svc.upsertInstrumentWithProfile(ctx, inst); err != nil {
		t.Fatalf("upsert instrument with profile: %v", err)
	}
	profile, err := svc.GetStockProfile(ctx, "301321")
	if err != nil {
		t.Fatalf("get profile: %v", err)
	}
	if profile.Name != inst.Name || !strings.Contains(profile.ProfileTextZh, "消费电子") {
		t.Fatalf("profile = %+v, want maintained from instrument", profile)
	}
}

func TestEnableBaseProfileMaintenanceSchedulesImmediateRun(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newStockProfileTestService(t)
	defer cleanup()
	if err := svc.Initialize(ctx); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	enabled := true
	updated, err := svc.CreateOrUpdateSettings(ctx, RequestCreateOrUpdateSettings{
		BaseProfileAutoMaintainEnabled: &enabled,
	})
	if err != nil {
		t.Fatalf("enable base profile maintenance: %v", err)
	}
	if !updated.BaseProfileAutoMaintainEnabled {
		t.Fatalf("base profile auto maintain disabled: %+v", updated)
	}
	if updated.BaseProfileNextMaintainAt.IsZero() || updated.BaseProfileNextMaintainAt.After(updated.UpdatedAt.Add(2*time.Second)) {
		t.Fatalf("next maintain at = %v, updated at = %v; want immediate first run", updated.BaseProfileNextMaintainAt, updated.UpdatedAt)
	}
	if updated.BaseProfileDeepUpdateBatchSize != defaultStockProfileDeepUpdateBatchSize ||
		updated.BaseProfileDeepUpdateAIBudget != defaultStockProfileDeepUpdateAIBudget ||
		updated.BaseProfileDeepUpdateRateLimitMs != defaultStockProfileDeepUpdateRateLimitMs {
		t.Fatalf("deep profile defaults = %+v", updated)
	}
}

func TestBaseProfileMaintenanceIntervalChangeRecomputesNextRun(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newStockProfileTestService(t)
	defer cleanup()
	if err := svc.Initialize(ctx); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	now := time.Now()
	settings := svc.settings
	settings.BaseProfileAutoMaintainEnabled = true
	settings.BaseProfileMaintainIntervalSeconds = 86400
	settings.BaseProfileLastMaintainAt = now.Add(-2 * time.Hour)
	settings.BaseProfileNextMaintainAt = now.Add(22 * time.Hour)
	if err := svc.store.CreateOrUpdateSettings(ctx, settings); err != nil {
		t.Fatalf("save existing settings: %v", err)
	}
	svc.settings = settings

	shorter := 3600
	updated, err := svc.CreateOrUpdateSettings(ctx, RequestCreateOrUpdateSettings{
		BaseProfileMaintainIntervalSeconds: &shorter,
	})
	if err != nil {
		t.Fatalf("shorten base profile interval: %v", err)
	}
	if updated.BaseProfileNextMaintainAt.IsZero() || updated.BaseProfileNextMaintainAt.After(time.Now().Add(2*time.Second)) {
		t.Fatalf("next maintain at = %v; want due after shortening interval", updated.BaseProfileNextMaintainAt)
	}
}

func TestSavingBaseProfileSettingsKeepsPendingImmediateRun(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newStockProfileTestService(t)
	defer cleanup()
	if err := svc.Initialize(ctx); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	now := time.Now()
	settings := svc.settings
	settings.BaseProfileAutoMaintainEnabled = true
	settings.BaseProfileMaintainIntervalSeconds = 86400
	settings.BaseProfileLastMaintainAt = time.Time{}
	settings.BaseProfileNextMaintainAt = now
	if err := svc.store.CreateOrUpdateSettings(ctx, settings); err != nil {
		t.Fatalf("save existing settings: %v", err)
	}
	svc.settings = settings

	batchSize := 24
	updated, err := svc.CreateOrUpdateSettings(ctx, RequestCreateOrUpdateSettings{
		BaseProfileDeepUpdateBatchSize: &batchSize,
	})
	if err != nil {
		t.Fatalf("save base profile batch size: %v", err)
	}
	if updated.BaseProfileNextMaintainAt.IsZero() || updated.BaseProfileNextMaintainAt.After(time.Now().Add(2*time.Second)) {
		t.Fatalf("next maintain at = %v; want pending immediate run preserved", updated.BaseProfileNextMaintainAt)
	}
}

func TestSavingBaseProfileSettingsUsesPersistedSettingsAsBase(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newStockProfileTestService(t)
	defer cleanup()
	if err := svc.Initialize(ctx); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	settings := svc.settings
	settings.AutoUpdateEnabled = true
	settings.UpdateIntervalSec = 7200
	settings.BaseProfileAutoMaintainEnabled = true
	settings.BaseProfileMaintainIntervalSeconds = 86400
	if err := svc.store.CreateOrUpdateSettings(ctx, settings); err != nil {
		t.Fatalf("save persisted settings: %v", err)
	}
	svc.settings = StockV2Settings{ID: "1", UpdateIntervalSec: 3600}

	batchSize := 24
	updated, err := svc.CreateOrUpdateSettings(ctx, RequestCreateOrUpdateSettings{
		BaseProfileDeepUpdateBatchSize: &batchSize,
	})
	if err != nil {
		t.Fatalf("save base profile settings: %v", err)
	}
	if !updated.AutoUpdateEnabled || updated.UpdateIntervalSec != 7200 {
		t.Fatalf("unrelated persisted settings were overwritten: %+v", updated)
	}
	if !updated.BaseProfileAutoMaintainEnabled || updated.BaseProfileDeepUpdateBatchSize != batchSize {
		t.Fatalf("base profile settings not saved: %+v", updated)
	}
}

func TestListStockProfileSummariesReturnsBatchAndMissing(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newStockProfileTestService(t)
	defer cleanup()

	if _, err := svc.store.UpsertStockProfile(ctx, StockProfile{
		Symbol:            "300750",
		Market:            "SZ",
		InstrumentType:    InstrumentTypeStock,
		Name:              "宁德时代",
		BusinessSummary:   "fallback summary",
		BusinessSummaryZh: "动力电池龙头",
		ProfileText:       "基础画像",
		AIProfileStatus:   StockProfileAIStatusReady,
		AIProfileModel:    "model-a",
	}); err != nil {
		t.Fatalf("upsert stock profile: %v", err)
	}

	got, err := svc.ListStockProfileSummaries(ctx, []string{"300750", "300750", "600519", ""})
	if err != nil {
		t.Fatalf("list summaries: %v", err)
	}
	if got["300750"].Status != "ready" ||
		got["300750"].BusinessSummary != "动力电池龙头" ||
		got["300750"].AIProfileStatus != StockProfileAIStatusReady {
		t.Fatalf("summary for 300750 = %+v", got["300750"])
	}
	if got["600519"].Status != "missing" || got["600519"].AIProfileStatus != StockProfileAIStatusMissing {
		t.Fatalf("summary for missing profile = %+v", got["600519"])
	}
}

func TestSavingBaseProfileSettingsStartsMissingBackgroundRunner(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newStockProfileTestService(t)
	defer cleanup()
	if err := svc.Initialize(ctx); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	now := time.Now()
	settings := svc.settings
	settings.BaseProfileAutoMaintainEnabled = true
	settings.BaseProfileMaintainIntervalSeconds = 3600
	settings.BaseProfileLastMaintainAt = now
	settings.BaseProfileNextMaintainAt = now.Add(time.Hour)
	if err := svc.store.CreateOrUpdateSettings(ctx, settings); err != nil {
		t.Fatalf("save existing settings: %v", err)
	}
	svc.settings = settings
	svc.StopBackground()

	batchSize := 24
	if _, err := svc.CreateOrUpdateSettings(ctx, RequestCreateOrUpdateSettings{
		BaseProfileDeepUpdateBatchSize: &batchSize,
	}); err != nil {
		t.Fatalf("save base profile settings: %v", err)
	}
	if svc.bgCancel == nil {
		t.Fatalf("background runner was not started")
	}
}

func TestAutomaticDeepStockProfileUpdateUsesPerSymbolAIRounds(t *testing.T) {
	ctx := context.Background()
	businessLine := "动力电池系统"
	svc, cleanup := newStockProfileTestServiceWithClient(t, stockProfileF10TestClient(&businessLine))
	defer cleanup()
	seedProfileInstruments(t, svc, ctx, "300750", "300751", "300752")
	configureStockProfileAgent(t, svc, ctx)
	svc.agentExecutor = fakeOperationReviewExecutor{
		pool:       svc.agentTaskPool,
		submit:     true,
		summary:    "profile enhanced",
		confidence: 0.8,
		result: map[string]any{
			"summaryZh": "AI 增强摘要",
		},
	}

	result, err := svc.runAutomaticDeepStockProfileUpdate(ctx, "test", stockProfileDeepUpdateOptions{
		SymbolBudget:      3,
		AIRoundsPerSymbol: 1,
		RateLimit:         0,
		Now:               time.Date(2026, 6, 24, 9, 0, 0, 0, time.UTC),
		RequestedBy:       "test",
	})
	if err != nil {
		t.Fatalf("run automatic deep profile update: %v", err)
	}
	if result.ProcessedCount != 3 || result.AICalledCount != 3 || result.AIRoundsPerSymbol != 1 || result.StoppedByBudget {
		t.Fatalf("result = %+v, want all symbols processed with per-symbol ai rounds", result)
	}
	runs, err := svc.ListAgentRuns(ctx, AgentRunListFilter{TaskType: AgentTaskTypeStockProfileSummary, Limit: 5})
	if err != nil {
		t.Fatalf("list agent runs: %v", err)
	}
	for _, run := range runs {
		_ = waitAgentRunTerminal(t, svc, run.ID)
	}
}

func TestAutomaticDeepStockProfileUpdateRollsQueue(t *testing.T) {
	ctx := context.Background()
	businessLine := "动力电池系统"
	svc, cleanup := newStockProfileTestServiceWithClient(t, stockProfileF10TestClient(&businessLine))
	defer cleanup()
	seedProfileInstruments(t, svc, ctx, "300750", "300751", "300752", "300753")

	for i := 0; i < 2; i++ {
		result, err := svc.runAutomaticDeepStockProfileUpdate(ctx, "test", stockProfileDeepUpdateOptions{
			SymbolBudget:      2,
			AIRoundsPerSymbol: 1,
			RateLimit:         0,
			Now:               time.Date(2026, 6, 24+i, 9, 0, 0, 0, time.UTC),
			RequestedBy:       "test",
		})
		if err != nil {
			t.Fatalf("run automatic deep profile update %d: %v", i, err)
		}
		if result.ProcessedCount != 2 {
			t.Fatalf("run %d result = %+v, want two processed", i, result)
		}
	}
	tasks, err := svc.ListStockProfileUpdateTasks(ctx, StockProfileUpdateTaskListFilter{Limit: 10})
	if err != nil {
		t.Fatalf("list profile update tasks: %v", err)
	}
	seen := map[string]bool{}
	for _, task := range tasks {
		seen[task.Symbol] = true
	}
	if len(seen) != 4 {
		t.Fatalf("updated symbols = %#v, want four unique symbols", seen)
	}
}

func TestRunAgentStockProfileSummaryUpdatesBilingualFields(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newStockProfileTestService(t)
	defer cleanup()

	if err := svc.store.UpsertInstrument(ctx, StockV2Instrument{
		ID:             "inst-300750",
		Symbol:         "300750",
		Market:         "SZ",
		InstrumentType: InstrumentTypeStock,
		Name:           "宁德时代",
		Industry:       "电力设备",
		Sector:         "新能源",
		Concepts:       []string{"锂电池"},
		Status:         "active",
	}); err != nil {
		t.Fatalf("upsert instrument: %v", err)
	}
	provider, err := svc.CreateAgentProviderProfile(ctx, RequestCreateAgentProviderProfile{ProviderType: AgentProviderTypeCodexCLI, Name: "codex-profile"})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	model, err := svc.CreateAgentModelProfile(ctx, RequestCreateAgentModelProfile{ProviderID: provider.ID, ModelName: "profile-model", Enabled: true})
	if err != nil {
		t.Fatalf("create model: %v", err)
	}
	if _, err := svc.UpdateAgentTaskProfile(ctx, AgentTaskTypeStockProfileSummary, RequestUpdateAgentTaskProfile{PrimaryModelID: &model.ID}); err != nil {
		t.Fatalf("bind profile task: %v", err)
	}
	svc.agentExecutor = fakeOperationReviewExecutor{
		pool:       svc.agentTaskPool,
		submit:     true,
		summary:    "profile enhanced",
		confidence: 0.8,
		result: map[string]any{
			"summaryZh":       "宁德时代是动力电池公司",
			"summaryEn":       "CATL is a power battery company",
			"aliasesEn":       []any{"CATL", "Contemporary Amperex Technology"},
			"keywordsEn":      []any{"lithium battery", "energy storage"},
			"businessLinesEn": []any{"EV batteries"},
		},
	}

	run, err := svc.RunAgentStockProfileSummary(ctx, "300750", "test")
	if err != nil {
		t.Fatalf("run profile agent: %v", err)
	}
	run = waitAgentRunTerminal(t, svc, run.ID)
	if run.Status != AgentRunStatusCompleted {
		t.Fatalf("run status = %s error=%s", run.Status, run.ErrorMessage)
	}
	profile, err := svc.GetStockProfile(ctx, "300750")
	if err != nil {
		t.Fatalf("get profile: %v", err)
	}
	if profile.AIProfileStatus != StockProfileAIStatusReady || profile.BusinessSummaryEn == "" {
		t.Fatalf("profile ai fields = %+v", profile)
	}
	tasks, err := svc.ListStockProfileUpdateTasks(ctx, StockProfileUpdateTaskListFilter{Symbol: "300750", Limit: 1})
	if err != nil {
		t.Fatalf("list profile update tasks: %v", err)
	}
	if len(tasks) != 1 || tasks[0].Status != StockProfileUpdateStatusCompleted || tasks[0].AIProfileStatus != StockProfileAIStatusReady {
		t.Fatalf("profile update task = %+v, want completed with ai ready", tasks)
	}
	for _, keyword := range []string{"CATL", "lithium battery", "EV batteries"} {
		if !strings.Contains(profile.ProfileText, keyword) {
			t.Fatalf("profile text %q missing %q", profile.ProfileText, keyword)
		}
	}
}

func TestRunAgentStockProfileSummaryMarksProfileFailedOnAgentError(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newStockProfileTestService(t)
	defer cleanup()

	if err := svc.store.UpsertInstrument(ctx, StockV2Instrument{
		ID:             "inst-300750",
		Symbol:         "300750",
		Market:         "SZ",
		InstrumentType: InstrumentTypeStock,
		Name:           "宁德时代",
		Industry:       "电力设备",
		Status:         "active",
	}); err != nil {
		t.Fatalf("upsert instrument: %v", err)
	}
	configureStockProfileAgent(t, svc, ctx)
	svc.agentExecutor = fakeOperationReviewExecutor{
		pool:    svc.agentTaskPool,
		submit:  false,
		execErr: errors.New("process exited (code 2) without submitting result"),
	}

	run, err := svc.RunAgentStockProfileSummary(ctx, "300750", "test")
	if err != nil {
		t.Fatalf("run profile agent: %v", err)
	}
	run = waitAgentRunTerminal(t, svc, run.ID)
	if run.Status != AgentRunStatusFailed {
		t.Fatalf("run status = %s, want failed", run.Status)
	}
	profile, err := svc.GetStockProfile(ctx, "300750")
	if err != nil {
		t.Fatalf("get profile: %v", err)
	}
	if profile.AIProfileStatus != StockProfileAIStatusFailed || !strings.Contains(profile.AIProfileError, "code 2") {
		t.Fatalf("profile ai status/error = %q/%q, want failed with code 2", profile.AIProfileStatus, profile.AIProfileError)
	}
	tasks, err := svc.ListStockProfileUpdateTasks(ctx, StockProfileUpdateTaskListFilter{Symbol: "300750", Limit: 1})
	if err != nil {
		t.Fatalf("list profile update tasks: %v", err)
	}
	if len(tasks) != 1 || tasks[0].Status != StockProfileUpdateStatusPartial || tasks[0].AIProfileStatus != StockProfileAIStatusFailed || !strings.Contains(tasks[0].AIProfileError, "code 2") {
		t.Fatalf("profile update task = %+v, want partial with ai failed", tasks)
	}
}

func TestListStockProfilesCanSearchProfileText(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newStockProfileTestService(t)
	defer cleanup()

	for _, inst := range []StockV2Instrument{
		{
			ID:             "inst-300750",
			Symbol:         "300750",
			Market:         "SZ",
			InstrumentType: InstrumentTypeStock,
			Name:           "宁德时代",
			Concepts:       []string{"锂电池"},
			Status:         "active",
		},
		{
			ID:             "inst-600000",
			Symbol:         "600000",
			Market:         "SH",
			InstrumentType: InstrumentTypeStock,
			Name:           "浦发银行",
			Industry:       "银行",
			Status:         "active",
		},
	} {
		if err := svc.store.UpsertInstrument(ctx, inst); err != nil {
			t.Fatalf("upsert instrument: %v", err)
		}
	}
	if _, err := svc.RebuildStockProfiles(ctx); err != nil {
		t.Fatalf("rebuild profiles: %v", err)
	}

	items, err := svc.ListStockProfiles(ctx, StockProfileListFilter{Keyword: "锂电池", Limit: 10})
	if err != nil {
		t.Fatalf("list profiles: %v", err)
	}
	if len(items) != 1 || items[0].Symbol != "300750" {
		t.Fatalf("items = %+v, want only 300750", items)
	}
}

func newStockProfileTestService(t *testing.T) (*Service, func()) {
	return newStockProfileTestServiceWithClient(t, nil)
}

func newStockProfileTestServiceWithClient(t *testing.T, client *http.Client) (*Service, func()) {
	t.Helper()
	store, err := NewStore(filepath.Join(t.TempDir(), "stockv2.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	svc := NewService(store, nil, client)
	return svc, func() {
		svc.StopBackground()
		if err := store.Close(); err != nil {
			t.Fatalf("close store: %v", err)
		}
	}
}

func seedProfileInstrument(t *testing.T, svc *Service, ctx context.Context) {
	t.Helper()
	if err := svc.store.UpsertInstrument(ctx, StockV2Instrument{
		ID:             "inst-300750",
		Symbol:         "300750",
		Market:         "SZ",
		InstrumentType: InstrumentTypeStock,
		Name:           "宁德时代",
		Status:         "active",
	}); err != nil {
		t.Fatalf("upsert instrument: %v", err)
	}
}

func seedProfileInstruments(t *testing.T, svc *Service, ctx context.Context, symbols ...string) {
	t.Helper()
	for _, symbol := range symbols {
		if err := svc.store.UpsertInstrument(ctx, StockV2Instrument{
			ID:             "inst-" + symbol,
			Symbol:         symbol,
			Market:         "SZ",
			InstrumentType: InstrumentTypeStock,
			Name:           "测试标的" + symbol,
			Status:         "active",
		}); err != nil {
			t.Fatalf("upsert instrument %s: %v", symbol, err)
		}
	}
}

func configureStockProfileAgent(t *testing.T, svc *Service, ctx context.Context) {
	t.Helper()
	provider, err := svc.CreateAgentProviderProfile(ctx, RequestCreateAgentProviderProfile{ProviderType: AgentProviderTypeCodexCLI, Name: "codex-profile"})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	model, err := svc.CreateAgentModelProfile(ctx, RequestCreateAgentModelProfile{ProviderID: provider.ID, ModelName: "profile-model", Enabled: true})
	if err != nil {
		t.Fatalf("create model: %v", err)
	}
	if _, err := svc.UpdateAgentTaskProfile(ctx, AgentTaskTypeStockProfileSummary, RequestUpdateAgentTaskProfile{PrimaryModelID: &model.ID}); err != nil {
		t.Fatalf("bind profile task: %v", err)
	}
}

func stockProfileF10TestClient(businessLine *string) *http.Client {
	return &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		line := "动力电池系统"
		if businessLine != nil && strings.TrimSpace(*businessLine) != "" {
			line = *businessLine
		}
		switch req.URL.Path {
		case "/PC_HSF10/CompanySurvey/CompanySurveyAjax":
			return stringResponse(http.StatusOK, `{"jbzl":{"gsmc":"宁德时代新能源科技股份有限公司","agjc":"宁德时代","ywmc":"Contemporary Amperex Technology Co., Limited","sshy":"电池","sszjhhy":"电气机械和器材制造业","gsjj":"公司是全球领先的新能源创新科技公司,主要从事动力电池和储能电池系统研发、生产和销售。","jyfw":"锂离子电池、动力电池系统、储能电池系统、电池材料及回收业务"}}`), nil
		case "/PC_HSF10/BusinessAnalysis/PageAjax":
			return stringResponse(http.StatusOK, `{"zygcfx":[{"REPORT_DATE":"2025-12-31","MAINOP_TYPE":"2","ITEM_NAME":"`+line+`","MBI_RATIO":"72.10"}]}`), nil
		case "/PC_HSF10/CoreConception/PageAjax":
			return stringResponse(http.StatusOK, `{"ssbk":[{"BOARD_NAME":"储能概念"}],"hxtc":[{"KEYWORD":"动力电池","KEY_CLASSIF":"主营业务"}]}`), nil
		default:
			return stringResponse(http.StatusNotFound, "not found"), nil
		}
	})}
}

func profileContainsString(items []string, want string) bool {
	return countProfileString(items, want) > 0
}

func countProfileString(items []string, want string) int {
	count := 0
	for _, item := range items {
		if item == want {
			count++
		}
	}
	return count
}
