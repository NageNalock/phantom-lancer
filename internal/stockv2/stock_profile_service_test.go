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

func TestAssetBaseProfileForStock(t *testing.T) {
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
	}); err != nil {
		t.Fatalf("upsert instrument: %v", err)
	}

	profile := refreshAssetBaseProfileForTest(t, svc, ctx, "300750")
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

func TestAssetBaseProfileForExchangeFund(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newStockProfileTestService(t)
	defer cleanup()

	if err := svc.store.UpsertInstrument(ctx, StockV2Instrument{
		ID:             "inst-510300",
		Symbol:         "510300",
		Market:         "SH",
		InstrumentType: InstrumentTypeExchangeFund,
		Name:           "沪深300ETF",
	}); err != nil {
		t.Fatalf("upsert instrument: %v", err)
	}

	profile := refreshAssetBaseProfileForTest(t, svc, ctx, "sh510300")
	if profile.FundType != "ETF" || profile.TrackingIndex != "沪深300" || profile.Theme != "宽基指数" {
		t.Fatalf("fund profile = %+v, want ETF fields", profile)
	}
	for _, keyword := range []string{"场内基金", "ETF", "沪深300", "宽基指数"} {
		if !profileContainsString(profile.Tags, keyword) && !strings.Contains(profile.ProfileText, keyword) {
			t.Fatalf("ETF profile missing keyword %q: %+v", keyword, profile)
		}
	}
}

func TestAssetBaseProfileEnrichesStockFromPublicF10(t *testing.T) {
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
	}); err != nil {
		t.Fatalf("upsert instrument: %v", err)
	}

	profile := refreshAssetBaseProfileForTest(t, svc, ctx, "300750")
	for _, want := range []string{"全球领先", "动力电池系统", "储能电池系统", "储能概念", "新能源车渗透率"} {
		if !strings.Contains(profile.ProfileText, want) {
			t.Fatalf("profile text %q missing %q; profile=%+v", profile.ProfileText, want, profile)
		}
	}
	if profileContainsString(profile.Concepts, "融资融券") {
		t.Fatalf("concepts = %#v, want noisy F10 concept filtered", profile.Concepts)
	}
}

func TestAssetBaseProfileEnrichesFundFromPublicF10(t *testing.T) {
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
	}); err != nil {
		t.Fatalf("upsert instrument: %v", err)
	}

	profile := refreshAssetBaseProfileForTest(t, svc, ctx, "169201")
	for _, want := range []string{"混合型-灵活", "事件驱动", "中微公司", "思瑞浦", "前十大持仓"} {
		if !strings.Contains(profile.ProfileText, want) && !strings.Contains(profile.ConstituentHint, want) {
			t.Fatalf("profile missing %q: %+v", want, profile)
		}
	}
}

func TestAssetProfileAISkipsWhenInputUnchanged(t *testing.T) {
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

	first := runAssetProfileAIForTest(t, svc, ctx, "300750", false)
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

	second := runAssetProfileAIForTest(t, svc, ctx, "300750", false)
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
	if len(tasks) != 3 {
		t.Fatalf("tasks len = %d, want 3", len(tasks))
	}
}

func TestAssetProfileAICallsWhenInputChanges(t *testing.T) {
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

	first := runAssetProfileAIForTest(t, svc, ctx, "300750", false)
	if first.AgentRun == nil {
		t.Fatalf("first result = %+v, want ai run", first)
	}
	_ = waitAgentRunTerminal(t, svc, first.AgentRun.ID)

	businessLine = "麒麟电池系统"
	second := runAssetProfileAIForTest(t, svc, ctx, "300750", false)
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

func TestAssetProfileAIUpdatesBilingualFields(t *testing.T) {
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

	result := runAssetProfileAIForTest(t, svc, ctx, "300750", true)
	if result.AgentRun == nil {
		t.Fatalf("profile ai result = %+v, want agent run", result)
	}
	run := waitAgentRunTerminal(t, svc, result.AgentRun.ID)
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
	tasks, err := svc.ListStockProfileUpdateTasks(ctx, StockProfileUpdateTaskListFilter{Symbol: "300750", Limit: 10})
	if err != nil {
		t.Fatalf("list profile update tasks: %v", err)
	}
	aiTask := stockProfileUpdateTaskByAgentRun(tasks, run.ID)
	if aiTask == nil || aiTask.Status != StockProfileUpdateStatusCompleted || aiTask.AIProfileStatus != StockProfileAIStatusReady {
		t.Fatalf("profile update task = %+v, want completed with ai ready for run %s", tasks, run.ID)
	}
	for _, keyword := range []string{"CATL", "lithium battery", "EV batteries"} {
		if !strings.Contains(profile.ProfileText, keyword) {
			t.Fatalf("profile text %q missing %q", profile.ProfileText, keyword)
		}
	}
}

func TestAssetProfileAIMarksProfileFailedOnAgentError(t *testing.T) {
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
	}); err != nil {
		t.Fatalf("upsert instrument: %v", err)
	}
	configureStockProfileAgent(t, svc, ctx)
	svc.agentExecutor = fakeOperationReviewExecutor{
		pool:    svc.agentTaskPool,
		submit:  false,
		execErr: errors.New("process exited (code 2) without submitting result"),
	}

	result := runAssetProfileAIForTest(t, svc, ctx, "300750", true)
	if result.AgentRun == nil {
		t.Fatalf("profile ai result = %+v, want agent run", result)
	}
	run := waitAgentRunTerminal(t, svc, result.AgentRun.ID)
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
	tasks, err := svc.ListStockProfileUpdateTasks(ctx, StockProfileUpdateTaskListFilter{Symbol: "300750", Limit: 10})
	if err != nil {
		t.Fatalf("list profile update tasks: %v", err)
	}
	aiTask := stockProfileUpdateTaskByAgentRun(tasks, run.ID)
	if aiTask == nil || aiTask.Status != StockProfileUpdateStatusPartial || aiTask.AIProfileStatus != StockProfileAIStatusFailed || !strings.Contains(aiTask.AIProfileError, "code 2") {
		t.Fatalf("profile update task = %+v, want partial with ai failed for run %s", tasks, run.ID)
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
		},
		{
			ID:             "inst-600000",
			Symbol:         "600000",
			Market:         "SH",
			InstrumentType: InstrumentTypeStock,
			Name:           "浦发银行",
			Industry:       "银行",
		},
	} {
		if err := svc.store.UpsertInstrument(ctx, inst); err != nil {
			t.Fatalf("upsert instrument: %v", err)
		}
		_ = refreshAssetBaseProfileForTest(t, svc, ctx, inst.Symbol)
	}

	items, err := svc.ListStockProfiles(ctx, StockProfileListFilter{Keyword: "锂电池", Limit: 10})
	if err != nil {
		t.Fatalf("list profiles: %v", err)
	}
	if len(items) != 1 || items[0].Symbol != "300750" {
		t.Fatalf("items = %+v, want only 300750", items)
	}
}

type assetProfileAIResult struct {
	Profile  StockProfile
	Task     StockProfileUpdateTask
	AgentRun *AgentRun
}

func refreshAssetBaseProfileForTest(t *testing.T, svc *Service, ctx context.Context, symbol string) StockProfile {
	t.Helper()
	normalized, _ := normalizeQuoteSymbolInput(symbol)
	if normalized == "" {
		normalized = strings.TrimSpace(symbol)
	}
	inst, err := svc.store.GetInstrument(ctx, normalized)
	if err != nil {
		t.Fatalf("get instrument %s: %v", symbol, err)
	}
	profile, _, _, err := svc.refreshAssetBaseProfile(ctx, inst)
	if err != nil {
		t.Fatalf("refresh asset base profile %s: %v", symbol, err)
	}
	return profile
}

func runAssetProfileAIForTest(t *testing.T, svc *Service, ctx context.Context, symbol string, force bool) assetProfileAIResult {
	t.Helper()
	normalized, _ := normalizeQuoteSymbolInput(symbol)
	if normalized == "" {
		normalized = strings.TrimSpace(symbol)
	}
	inst, err := svc.store.GetInstrument(ctx, normalized)
	if err != nil {
		t.Fatalf("get instrument %s: %v", symbol, err)
	}
	profile, previous, sourceStatuses, err := svc.refreshAssetBaseProfile(ctx, inst)
	if err != nil {
		t.Fatalf("refresh asset base profile %s: %v", symbol, err)
	}
	item := AssetMaintenanceItem{
		Symbol:                profile.Symbol,
		Market:                profile.Market,
		InstrumentType:        profile.InstrumentType,
		Name:                  profile.Name,
		BaseProfileHashBefore: previous.BaseProfileHash,
		BaseProfileHashAfter:  profile.BaseProfileHash,
		BaseProfileChanged:    previous.BaseProfileHash == "" || previous.BaseProfileHash != profile.BaseProfileHash,
		StartedAt:             time.Now(),
	}
	agentRun, _, _, err := svc.maybeRunAssetProfileAI(ctx, profile, previous, item, nil, sourceStatuses, assetMaintenanceOptions{
		TriggerSource: StockProfileUpdateTriggerManual,
		RequestedBy:   "test",
		ForceAI:       force,
		Budget:        &assetMaintenanceBudget{AIRemaining: 1},
	})
	if err != nil && agentRun == nil {
		t.Fatalf("run asset profile ai %s: %v", symbol, err)
	}
	tasks, err := svc.ListStockProfileUpdateTasks(ctx, StockProfileUpdateTaskListFilter{Symbol: normalized, Limit: 10})
	if err != nil {
		t.Fatalf("list profile update tasks %s: %v", symbol, err)
	}
	if len(tasks) == 0 {
		t.Fatalf("profile update tasks missing for %s", symbol)
	}
	task := tasks[0]
	if agentRun != nil {
		for _, candidate := range tasks {
			if candidate.AgentRunID == agentRun.ID {
				task = candidate
				break
			}
		}
	}
	return assetProfileAIResult{Profile: profile, Task: task, AgentRun: agentRun}
}

func stockProfileUpdateTaskByAgentRun(tasks []StockProfileUpdateTask, runID string) *StockProfileUpdateTask {
	for i := range tasks {
		if tasks[i].AgentRunID == runID {
			return &tasks[i]
		}
	}
	return nil
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
