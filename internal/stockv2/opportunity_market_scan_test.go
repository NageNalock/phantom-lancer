package stockv2

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestOpportunityMarketScanMainBoardBoundary(t *testing.T) {
	tests := []struct {
		item StockV2Instrument
		want bool
	}{
		{StockV2Instrument{Symbol: "600000", Market: "SH", InstrumentType: InstrumentTypeStock, Name: "浦发银行"}, true},
		{StockV2Instrument{Symbol: "605001", Market: "SH", InstrumentType: InstrumentTypeStock, Name: "威奥股份"}, true},
		{StockV2Instrument{Symbol: "002230", Market: "SZ", InstrumentType: InstrumentTypeStock, Name: "科大讯飞"}, true},
		{StockV2Instrument{Symbol: "300750", Market: "SZ", InstrumentType: InstrumentTypeStock, Name: "宁德时代"}, false},
		{StockV2Instrument{Symbol: "688981", Market: "SH", InstrumentType: InstrumentTypeStock, Name: "中芯国际"}, false},
		{StockV2Instrument{Symbol: "600001", Market: "SH", InstrumentType: InstrumentTypeStock, Name: "ST测试"}, false},
		{StockV2Instrument{Symbol: "600002", Market: "SH", InstrumentType: InstrumentTypeExchangeFund, Name: "测试ETF"}, false},
	}
	for _, tt := range tests {
		if got := isOpportunityMainBoardInstrument(tt.item); got != tt.want {
			t.Errorf("isOpportunityMainBoardInstrument(%s %s)=%v, want %v", tt.item.Symbol, tt.item.Name, got, tt.want)
		}
	}
}

func TestOpportunityMarketThemeSemanticRecallAdmitsMainBoardCandidate(t *testing.T) {
	svc, cleanup := newEmbeddingTestService(t)
	defer cleanup()
	ctx := context.Background()
	configureEmbeddingModel(t, svc, "embed-market-theme")

	for _, profile := range []StockProfile{
		{Symbol: "600196", Market: "SH", InstrumentType: InstrumentTypeStock, Name: "复星医药", ProfileText: "疫苗研发与医药产业服务"},
		{Symbol: "300122", Market: "SZ", InstrumentType: InstrumentTypeStock, Name: "智飞生物", ProfileText: "疫苗研发与医药产业服务"},
	} {
		if _, err := svc.store.UpsertStockProfile(ctx, profile); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := svc.RebuildEmbeddingAssets(ctx, RequestRebuildEmbeddingAssets{ObjectTypes: []string{EmbeddingObjectStockProfile}}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	thread, err := svc.store.CreateNewsThread(ctx, NewsThread{
		Title: "Moderna 与默沙东癌症疫苗三期达标", CoreThesis: "个性化 mRNA 癌症疫苗出现关键临床进展",
		Stage: NewsThreadStageAccelerating, Status: NewsThreadStatusActive, Confidence: .72, LastChangedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	version, err := svc.store.CreateNewsThreadVersion(ctx, NewsThreadVersion{
		ThreadID: thread.ID, RunID: "run-mrna", WindowType: NewsContextWindowFourHour, VersionNo: 1,
		Title: thread.Title, CoreThesis: thread.CoreThesis, Stage: thread.Stage, Confidence: thread.Confidence,
		MaterialChange: true, Symbols: []string{"MRNA.O", "MRK.N"}, Industries: []string{"肿瘤疫苗", "mRNA平台"},
		ReviewStatus: NewsContextReviewNotRequired, EffectiveAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	scored := []opportunityMarketScanRawMetric{{
		Instrument:     StockV2Instrument{Symbol: "600196", Market: "SH", InstrumentType: InstrumentTypeStock, Name: "复星医药"},
		PrefilterScore: 20,
	}}
	matches, snapshot := svc.loadOpportunityMarketThemeMatches(ctx, map[string]StockProfile{
		"600196": {Symbol: "600196", Market: "SH", InstrumentType: InstrumentTypeStock, Name: "复星医药", ProfileText: "疫苗研发与医药产业服务"},
	}, scored, now.Add(time.Second))
	if snapshot.Status != DecisionHealthHealthy || snapshot.VersionCount != 1 || snapshot.SemanticQueryCount != 1 {
		t.Fatalf("theme snapshot=%#v", snapshot)
	}
	if len(matches["600196"]) != 1 || matches["600196"][0].VersionID != version.ID ||
		matches["600196"][0].MatchKind != OpportunityMarketThemeMatchSemantic || !matches["600196"][0].RequiresCausalVerification {
		t.Fatalf("semantic theme matches=%#v", matches)
	}
	if score, _, _ := opportunityMarketThemeScoreFromMatches(matches["600196"], time.Time{}); score != 0 {
		t.Fatalf("semantic-only match score=%v, want no unverified theme boost", score)
	}
	if _, ok := matches["300122"]; ok {
		t.Fatalf("ChiNext candidate escaped the bounded main-board universe: %#v", matches["300122"])
	}
	thread.Stage = NewsThreadStageRetreating
	if _, err := svc.store.UpdateNewsThread(ctx, thread); err != nil {
		t.Fatal(err)
	}
	matches, snapshot = svc.loadOpportunityMarketThemeMatches(ctx, map[string]StockProfile{
		"600196": {Symbol: "600196", Market: "SH", InstrumentType: InstrumentTypeStock, Name: "复星医药", ProfileText: "疫苗研发与医药产业服务"},
	}, scored, now.Add(2*time.Second))
	if snapshot.VersionCount != 0 || len(matches) != 0 {
		t.Fatalf("retreating current theme was still recalled: snapshot=%#v matches=%#v", snapshot, matches)
	}
}

func TestOpportunityMarketPrefilterReservesMessageCandidateBeyondPriceCutoff(t *testing.T) {
	scored := make([]opportunityMarketScanRawMetric, opportunityMarketScanLocalLimit+1)
	for i := range scored {
		symbol := fmt.Sprintf("%06d", i+1)
		scored[i] = opportunityMarketScanRawMetric{
			Instrument:     StockV2Instrument{Symbol: symbol, Market: "SZ", InstrumentType: InstrumentTypeStock, Name: symbol},
			PrefilterScore: float64(len(scored) - i),
		}
	}
	target := scored[len(scored)-1].Instrument.Symbol
	matches := map[string][]OpportunityMarketThemeMatch{target: {{
		ThreadID: "thread-mrna", VersionID: "version-mrna", Title: "癌症疫苗进展",
		MatchKind: OpportunityMarketThemeMatchSemantic, RequiresCausalVerification: true,
	}}}
	selected, ranks, admissions := selectOpportunityMarketPrefilter(scored, matches, OpportunityMarketSectorSnapshot{})
	if len(selected) != opportunityMarketScanLocalLimit || selected[0].Instrument.Symbol != target || ranks[target] != opportunityMarketScanLocalLimit+1 {
		t.Fatalf("selected first=%s len=%d rank=%d", selected[0].Instrument.Symbol, len(selected), ranks[target])
	}
	if lane := opportunityMarketSourceLane(admissions[target]); lane != OpportunityMarketScanSourceMessage {
		t.Fatalf("source lane=%q, want message", lane)
	}
}

func TestOpportunityMarketSectorSnapshotDetectsEmergenceAndPersistsConfirmation(t *testing.T) {
	scored := make([]opportunityMarketScanRawMetric, 0, 6)
	for i := 0; i < 6; i++ {
		scored = append(scored, opportunityMarketScanRawMetric{
			Instrument: StockV2Instrument{Symbol: fmt.Sprintf("600%03d", i), Market: "SH", InstrumentType: InstrumentTypeStock, Name: fmt.Sprintf("农业%d", i), Industry: "种植业", Sector: "农林牧渔"},
			Close:      10.8 + float64(i)/10, Close3: 10.4, Close5: 10.5, Close20: 9.8,
			MA20: 10.6, MA20Prev3: 10.9, Volume5: 1200, Volume20: 1000,
			PrefilterScore: 90 - float64(i),
		})
	}
	first, signals := buildOpportunityMarketSectorSnapshot(scored, OpportunityMarketSectorSnapshot{}, "2026-08-10", time.Now())
	if first.Status != DecisionHealthHealthy || first.CoverageRatio != 1 || len(first.Trends) != 2 {
		t.Fatalf("first sector snapshot=%+v", first)
	}
	for _, trend := range first.Trends {
		if trend.State != OpportunityMarketSectorStateEmerging || trend.FirstSeenTradeDate != "2026-08-10" || trend.Streak != 1 {
			t.Fatalf("emerging trend=%+v", trend)
		}
	}
	if len(signals["600000"]) != 2 {
		t.Fatalf("sector signals=%+v", signals["600000"])
	}
	second, _ := buildOpportunityMarketSectorSnapshot(scored, first, "2026-08-11", time.Now())
	for _, trend := range second.Trends {
		if trend.State != OpportunityMarketSectorStateConfirmed || trend.FirstSeenTradeDate != "2026-08-10" || trend.Streak != 2 {
			t.Fatalf("confirmed trend=%+v", trend)
		}
	}
}

func TestOpportunityMarketSectorSnapshotSeparatesDegradedAndBlockedCoverage(t *testing.T) {
	items := make([]opportunityMarketScanRawMetric, 10)
	for i := range items {
		items[i].Instrument.Symbol = fmt.Sprintf("600%03d", i)
		if i < 9 {
			items[i].Instrument.Industry = "测试行业"
		}
	}
	degraded, _ := buildOpportunityMarketSectorSnapshot(items, OpportunityMarketSectorSnapshot{}, "2026-08-10", time.Now())
	if degraded.Status != DecisionHealthDegraded || math.Abs(degraded.CoverageRatio-.9) > .0001 {
		t.Fatalf("degraded snapshot=%+v", degraded)
	}
	for i := 7; i < 9; i++ {
		items[i].Instrument.Industry = ""
	}
	blocked, _ := buildOpportunityMarketSectorSnapshot(items, OpportunityMarketSectorSnapshot{}, "2026-08-10", time.Now())
	if blocked.Status != DecisionHealthBlocked || math.Abs(blocked.CoverageRatio-.7) > .0001 {
		t.Fatalf("blocked snapshot=%+v", blocked)
	}
}

func TestOpportunityMarketSectorAdmissionReservesRepresentativeBeyondPriceLane(t *testing.T) {
	scored := make([]opportunityMarketScanRawMetric, opportunityMarketScanLocalLimit+1)
	for i := range scored {
		symbol := fmt.Sprintf("%06d", i+1)
		scored[i] = opportunityMarketScanRawMetric{Instrument: StockV2Instrument{Symbol: symbol}, PrefilterScore: float64(len(scored) - i)}
	}
	target := scored[len(scored)-1].Instrument.Symbol
	snapshot := OpportunityMarketSectorSnapshot{Trends: []OpportunityMarketSectorTrend{{
		Key: "industry:种植业", Name: "种植业", State: OpportunityMarketSectorStateEmerging,
		RepresentativeSymbols: []string{target},
	}}}
	selected, _, admissions := selectOpportunityMarketPrefilter(scored, nil, snapshot)
	if selected[0].Instrument.Symbol != target || !admissions[target].sector || admissions[target].price {
		t.Fatalf("first=%s admission=%+v", selected[0].Instrument.Symbol, admissions[target])
	}
	if lane := opportunityMarketSourceLane(admissions[target]); lane != OpportunityMarketScanSourceSector {
		t.Fatalf("source lane=%q, want sector", lane)
	}
}

func TestOpportunityMarketThemeStructuredMatchKeepsExactProvenance(t *testing.T) {
	now := time.Now()
	version := NewsThreadVersion{
		ID: "version-mrna", ThreadID: "thread-mrna", Title: "癌症疫苗三期达标",
		CoreThesis: "mRNA 肿瘤疫苗取得关键临床进展", Stage: NewsThreadStageAccelerating,
		Confidence: .7, MaterialChange: true, EffectiveAt: now, Symbols: []string{"MRNA.O"}, Industries: []string{"肿瘤疫苗"},
	}
	match, ok := deterministicOpportunityMarketThemeMatch(version, StockProfile{
		Symbol: "600196", Name: "复星医药", Concepts: []string{"mRNA"}, KeywordsZh: []string{"癌症疫苗"},
	})
	if !ok || match.ThreadID != version.ThreadID || match.VersionID != version.ID ||
		match.MatchKind != OpportunityMarketThemeMatchStructured || !match.RequiresCausalVerification {
		t.Fatalf("structured match=%#v ok=%v", match, ok)
	}
}

func TestOpportunityMarketThemeRefreshRunsOnceForLateMaterialTheme(t *testing.T) {
	store := newTestStore(t)
	svc := NewService(store, nil, nil)
	defer svc.Close()
	ctx := context.Background()
	now := time.Now()
	capturedAt := now.Add(-time.Hour)
	base, err := store.CreateOpportunityMarketScanRun(ctx, OpportunityMarketScanRun{
		TriggerType: OpportunityMarketScanTriggerScheduled, Status: OpportunityMarketScanStatusCompleted,
		TradeDate: "2026-08-20", ThemeSnapshot: OpportunityMarketThemeSnapshot{CapturedAt: capturedAt, Status: DecisionHealthHealthy},
	})
	if err != nil {
		t.Fatal(err)
	}
	base.FinishedAt = capturedAt.Add(20 * time.Minute)
	if _, err := store.UpdateOpportunityMarketScanRun(ctx, base); err != nil {
		t.Fatal(err)
	}
	thread, err := store.CreateNewsThread(ctx, NewsThread{
		Title: "盘后创新药催化", CoreThesis: "临床结果出现实质变化", Stage: NewsThreadStageEmerging,
		Status: NewsThreadStatusActive, Confidence: .7, LastChangedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateNewsThreadVersion(ctx, NewsThreadVersion{
		ThreadID: thread.ID, RunID: "late-theme-run", WindowType: NewsContextWindowFourHour,
		VersionNo: 1, Title: thread.Title, CoreThesis: thread.CoreThesis, Stage: thread.Stage,
		Confidence: thread.Confidence, MaterialChange: true, EffectiveAt: now.Add(-10 * time.Minute),
		ReviewStatus: NewsContextReviewNotRequired,
	}); err != nil {
		t.Fatal(err)
	}
	if !svc.shouldStartOpportunityMarketThemeRefresh(ctx, base.TradeDate, now) {
		t.Fatal("late material theme did not request one bounded refresh")
	}
	if _, err := store.CreateOpportunityMarketScanRun(ctx, OpportunityMarketScanRun{
		TriggerType: OpportunityMarketScanTriggerThemeRefresh, Status: OpportunityMarketScanStatusFailed,
		TradeDate: base.TradeDate,
	}); err != nil {
		t.Fatal(err)
	}
	if svc.shouldStartOpportunityMarketThemeRefresh(ctx, base.TradeDate, now) {
		t.Fatal("theme refresh was scheduled more than once for the same trading date")
	}
}

func TestOpportunityMarketScanMetricsAndCoverage(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()
	ctx := context.Background()
	for _, instrument := range []StockV2Instrument{
		{ID: "main-a", Symbol: "600000", Market: "SH", InstrumentType: InstrumentTypeStock, Name: "主板甲", Industry: "银行"},
		{ID: "main-b", Symbol: "000001", Market: "SZ", InstrumentType: InstrumentTypeStock, Name: "主板乙", Industry: "银行"},
		{ID: "growth", Symbol: "300750", Market: "SZ", InstrumentType: InstrumentTypeStock, Name: "创业板", Industry: "电力设备"},
	} {
		if err := store.UpsertInstrument(ctx, instrument); err != nil {
			t.Fatalf("upsert instrument: %v", err)
		}
	}
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.Local)
	var bars []StockV2DailyBar
	for _, symbol := range []string{"600000", "000001", "300750"} {
		market := inferAStockMarket(symbol)
		for i := 0; i < 65; i++ {
			closePrice := 10 + float64(i)*0.05
			bars = append(bars, StockV2DailyBar{
				ID: generateID(), Symbol: symbol, Market: market,
				TradeDate: start.AddDate(0, 0, i).Format("2006-01-02"),
				Open:      closePrice - .02, High: closePrice + .1, Low: closePrice - .1, Close: closePrice,
				PrevClose: closePrice - .05, Volume: 1000 + float64(i), Amount: 0,
				PctChange: .4, Adjusted: DailyBarAdjustedNone, Source: "unit_test", FetchedAt: time.Now(), Quality: DailyBarQualityOK,
			})
		}
	}
	if err := store.UpsertDailyBars(ctx, bars); err != nil {
		t.Fatalf("upsert daily bars: %v", err)
	}
	raw, err := store.marketDB.LoadOpportunityMarketScanMetrics(ctx, "")
	if err != nil {
		t.Fatalf("load scan metrics: %v", err)
	}
	tradeDate, universe, covered := opportunityMarketScanCoverage(raw)
	if tradeDate != start.AddDate(0, 0, 64).Format("2006-01-02") || universe != 2 || covered != 2 {
		t.Fatalf("coverage=%s %d/%d, want latest and 2/2", tradeDate, covered, universe)
	}
	scored := scoreOpportunityMarketScanMetrics(raw)
	if len(scored) != 2 || !isOpportunityMainBoardInstrument(scored[0].Instrument) || math.IsNaN(scored[0].PrefilterScore) {
		t.Fatalf("scored=%+v, want two finite main-board candidates", scored)
	}
	if scored[0].MedianAmount20 <= 0 {
		t.Fatalf("median amount proxy=%v, want positive value for amount-less Tencent bars", scored[0].MedianAmount20)
	}
}

func TestOpportunityMarketScanCoverageIgnoresOpenSessionDailyRow(t *testing.T) {
	raw := []opportunityMarketScanRawMetric{
		{Instrument: StockV2Instrument{Symbol: "600000", Market: "SH", InstrumentType: InstrumentTypeStock, Name: "主板甲"}, TradeDate: "2026-08-25", RowCount: 61},
		{Instrument: StockV2Instrument{Symbol: "000001", Market: "SZ", InstrumentType: InstrumentTypeStock, Name: "主板乙"}, TradeDate: "2026-08-24", RowCount: 60},
	}
	loc := time.FixedZone("Asia/Shanghai", 8*60*60)
	tradeDate, universe, covered := opportunityMarketScanCoverageAt(raw, time.Date(2026, 8, 25, 12, 0, 0, 0, loc))
	if tradeDate != "2026-08-24" || universe != 2 || covered != 2 {
		t.Fatalf("pre-close coverage=%s %d/%d, want 2026-08-24 2/2", tradeDate, covered, universe)
	}
	tradeDate, universe, covered = opportunityMarketScanCoverageAt(raw, time.Date(2026, 8, 25, 16, 0, 0, 0, loc))
	if tradeDate != "2026-08-25" || universe != 2 || covered != 1 {
		t.Fatalf("post-close coverage=%s %d/%d, want 2026-08-25 1/2", tradeDate, covered, universe)
	}
}

func TestOpportunityMarketScanStatusBlocksOnFailedUniverseMaintenance(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()
	ctx := context.Background()
	for _, instrument := range []StockV2Instrument{
		{ID: "main-a", Symbol: "600000", Market: "SH", InstrumentType: InstrumentTypeStock, Name: "主板甲"},
		{ID: "main-b", Symbol: "000001", Market: "SZ", InstrumentType: InstrumentTypeStock, Name: "主板乙"},
	} {
		if err := store.UpsertInstrument(ctx, instrument); err != nil {
			t.Fatal(err)
		}
		bars := make([]StockV2DailyBar, 65)
		for i := range bars {
			bars[i] = StockV2DailyBar{
				Symbol: instrument.Symbol, Market: instrument.Market,
				TradeDate: time.Date(2026, 6, 21, 0, 0, 0, 0, time.Local).AddDate(0, 0, i).Format("2006-01-02"),
				Open:      10, High: 11, Low: 9, Close: 10.5, PrevClose: 10, Volume: 1000, Amount: 100000,
				Adjusted: DailyBarAdjustedNone, Source: "unit_test", FetchedAt: time.Now(), Quality: DailyBarQualityOK,
			}
		}
		if err := store.UpsertDailyBars(ctx, bars); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.CreateUpdateJob(ctx, StockV2UpdateJob{
		ID: "failed-maintenance", TriggerType: "scheduled", TriggerSource: "test", Status: "failed",
		TotalCount: 100, ProcessedCount: 20, SuccessCount: 10, FailedCount: 3,
		ErrorMessage: "daily bars source circuit is open", StartAt: time.Now().Add(-time.Minute), EndAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	svc := NewService(store, nil, nil)
	defer svc.StopBackground()
	status, err := svc.GetOpportunityMarketScanStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.Ready || status.CoverageRatio != 1 || !strings.Contains(status.BlockedReason, "最近全市场数据维护失败") || !strings.Contains(status.BlockedReason, "10 / 共 100") {
		t.Fatalf("status = %+v", status)
	}
}

func TestApplyOpportunityQFQMetricsEstimatesAmountWhenMissing(t *testing.T) {
	bars := make([]StockV2DailyBar, 65)
	for i := range bars {
		bars[i] = StockV2DailyBar{
			TradeDate: time.Date(2026, 1, 1+i, 0, 0, 0, 0, time.Local).Format("2006-01-02"),
			Close:     10 + float64(i)/100, Volume: 1000 + float64(i),
		}
	}
	var metrics OpportunityMarketScanMetrics
	applyOpportunityQFQMetrics(&metrics, bars)
	if !metrics.QFQAvailable || metrics.MedianAmount20 <= 0 {
		t.Fatalf("metrics=%+v, want QFQ data with positive amount proxy", metrics)
	}
}

func TestOpportunityMarketFactorClustersKeepHigherRankedCandidate(t *testing.T) {
	candidates := []OpportunityMarketScanCandidate{
		{Symbol: "600001", Name: "高排名", Industry: "有色金属"},
		{Symbol: "600002", Name: "低排名", Industry: "有色金属"},
	}
	bars := map[string][]StockV2DailyBar{"600001": {}, "600002": {}}
	for i := 0; i < 60; i++ {
		date := time.Date(2026, 1, 1+i, 0, 0, 0, 0, time.Local).Format("2006-01-02")
		change := float64((i%7)-3) / 10
		bars["600001"] = append(bars["600001"], StockV2DailyBar{TradeDate: date, PctChange: change})
		bars["600002"] = append(bars["600002"], StockV2DailyBar{TradeDate: date, PctChange: change})
	}
	clusters, blocked := opportunityMarketFactorClusters(candidates, bars)
	if clusters["600002"] != "600001" || !strings.Contains(blocked["600002"], "高排名") || blocked["600001"] != "" {
		t.Fatalf("clusters=%+v blocked=%+v", clusters, blocked)
	}
}

func TestOpportunityMarketScanRepositoryDefaultsAndCandidates(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()
	ctx := context.Background()
	config, err := store.GetOpportunityMarketScanConfig(ctx)
	if err != nil || config.Enabled {
		t.Fatalf("default config=%+v err=%v, want disabled", config, err)
	}
	run, err := store.CreateOpportunityMarketScanRun(ctx, OpportunityMarketScanRun{
		TriggerType:   OpportunityMarketScanTriggerManual,
		ThemeSnapshot: OpportunityMarketThemeSnapshot{Status: DecisionHealthDegraded, VersionIDs: []string{"theme-version-1"}, VersionCount: 1},
		SectorSnapshot: OpportunityMarketSectorSnapshot{
			Status: DecisionHealthHealthy, TradeDate: "2026-08-10",
			Trends: []OpportunityMarketSectorTrend{{
				Key: "industry:种植业", Name: "种植业", State: OpportunityMarketSectorStateEmerging, FirstSeenTradeDate: "2026-08-10", Streak: 1,
			}},
		},
	})
	if err != nil {
		t.Fatalf("create scan run: %v", err)
	}
	storedRun, err := store.GetOpportunityMarketScanRun(ctx, run.ID)
	if err != nil || storedRun.ThemeSnapshot.VersionCount != 1 || len(storedRun.ThemeSnapshot.VersionIDs) != 1 ||
		len(storedRun.SectorSnapshot.Trends) != 1 || storedRun.SectorSnapshot.Trends[0].State != OpportunityMarketSectorStateEmerging {
		t.Fatalf("stored theme snapshot=%#v err=%v", storedRun.ThemeSnapshot, err)
	}
	if err := store.UpsertOpportunityMarketScanCandidates(ctx, []OpportunityMarketScanCandidate{{
		ID: "candidate-a", ScanRunID: run.ID, Symbol: "600000", Market: "SH", Name: "浦发银行",
		Stage: OpportunityMarketScanCandidateResearch, PrefilterScore: 70, FinalScore: 75,
		Metrics: OpportunityMarketScanMetrics{TradeDate: "2026-08-10", QFQAvailable: true, DecisionStatus: DecisionHealthDegraded, SourceLane: OpportunityMarketScanSourceMessage, AdmissionReasons: []string{OpportunityMarketScanSourceSector, OpportunityMarketScanSourceMessage}},
	}}); err != nil {
		t.Fatalf("upsert candidate: %v", err)
	}
	items, err := store.ListOpportunityMarketScanCandidates(ctx, OpportunityMarketScanCandidateListFilter{ScanRunID: run.ID, Limit: 10})
	if err != nil || len(items) != 1 || !items[0].Metrics.QFQAvailable {
		t.Fatalf("candidates=%+v err=%v", items, err)
	}
	items, err = store.ListOpportunityMarketScanCandidates(ctx, OpportunityMarketScanCandidateListFilter{ScanRunID: run.ID, DecisionStatus: DecisionHealthDegraded, Limit: 10})
	if err != nil || len(items) != 1 {
		t.Fatalf("degraded candidates=%+v err=%v", items, err)
	}
	items, err = store.ListOpportunityMarketScanCandidates(ctx, OpportunityMarketScanCandidateListFilter{ScanRunID: run.ID, DecisionStatus: DecisionHealthHealthy, Limit: 10})
	if err != nil || len(items) != 0 {
		t.Fatalf("healthy candidates=%+v err=%v, want none", items, err)
	}
	items, err = store.ListOpportunityMarketScanCandidates(ctx, OpportunityMarketScanCandidateListFilter{ScanRunID: run.ID, SourceLane: OpportunityMarketScanSourceMessage, Limit: 10})
	if err != nil || len(items) != 1 {
		t.Fatalf("message candidates=%+v err=%v", items, err)
	}
	items, err = store.ListOpportunityMarketScanCandidates(ctx, OpportunityMarketScanCandidateListFilter{ScanRunID: run.ID, SourceLane: "sector_related", Limit: 10})
	if err != nil || len(items) != 1 {
		t.Fatalf("sector candidates=%+v err=%v", items, err)
	}
	if err := store.UpsertOpportunityMarketScanCandidates(ctx, []OpportunityMarketScanCandidate{{
		ID: "candidate-legacy", ScanRunID: run.ID, Symbol: "600001", Market: "SH", Name: "历史候选",
		Stage: OpportunityMarketScanCandidateResearch, Metrics: OpportunityMarketScanMetrics{TradeDate: "2026-08-10"},
	}}); err != nil {
		t.Fatal(err)
	}
	items, err = store.ListOpportunityMarketScanCandidates(ctx, OpportunityMarketScanCandidateListFilter{ScanRunID: run.ID, SourceLane: OpportunityMarketScanSourcePrice, Limit: 10})
	if err != nil || len(items) != 1 || items[0].Symbol != "600001" {
		t.Fatalf("legacy price candidates=%+v err=%v", items, err)
	}
}

func TestOpportunityMarketScanDecisionReasonsDistinguishFilterStages(t *testing.T) {
	items := []OpportunityMarketScanCandidate{
		{Symbol: "000001", Stage: OpportunityMarketScanCandidateExcluded, ExclusionReason: "前复权短期涨幅超过风险边界", StrategyStatus: OpportunityMarketScanStrategySkipped, Metrics: OpportunityMarketScanMetrics{Return5Pct: 19.2, Return20Pct: 36.8}},
		{Symbol: "000002", Stage: OpportunityMarketScanCandidateReviewedOut, StrategyStatus: OpportunityMarketScanStrategySkipped},
		{Symbol: "000003", Stage: OpportunityMarketScanCandidateFinal, StrategyStatus: OpportunityMarketScanStrategySkipped},
		{Symbol: "000004", Stage: OpportunityMarketScanCandidateFinal, StrategyStatus: OpportunityMarketScanStrategySkipped},
		{Symbol: "000005", Stage: OpportunityMarketScanCandidateFinal, StrategyStatus: OpportunityMarketScanStrategySkipped},
		{Symbol: "000006", Stage: OpportunityMarketScanCandidateFinal, StrategyStatus: OpportunityMarketScanStrategyGenerated},
	}
	applyOpportunityMarketScanDecisionReasons(items,
		map[string]string{"000002": "业务与扫描主题没有直接映射"},
		map[string]string{"000003": "缺少当期公司级强证据"},
		map[string]OpportunityCandidate{
			"000003": {Symbol: "000003", EvidenceScore: 70, Confidence: .7, Status: OpportunityCandidateStatusStrategyRequested},
			"000004": {Symbol: "000004", EvidenceScore: 50, Confidence: .6, Status: OpportunityCandidateStatusCandidate},
			"000005": {Symbol: "000005", EvidenceScore: 70, Confidence: .7, Status: OpportunityCandidateStatusCandidate, HorizonOutlooks: testModelHorizonOutlooks(10)},
		},
	)
	want := []string{
		"前复权短期涨幅超过风险边界",
		"业务与扫描主题没有直接映射",
		"缺少当期公司级强证据",
		"未达策略门槛：证据 50/55，置信度 0.60/0.55",
		fmt.Sprintf("Agent 证据排名未进入策略草拟前 %d", opportunityMarketScanStrategyLimit),
		"已生成未激活策略草案",
	}
	for i := range want {
		if items[i].DecisionReason != want[i] {
			t.Fatalf("items[%d].decisionReason=%q, want %q", i, items[i].DecisionReason, want[i])
		}
	}
	if len(items[4].HorizonOutlooks) != 3 {
		t.Fatalf("final candidate horizon outlooks = %#v, want hydrated model forecast", items[4].HorizonOutlooks)
	}
}

func TestListOpportunityMarketScanCandidatesRestoresSavedDecisionReasons(t *testing.T) {
	store := newTestStore(t)
	svc := NewService(store, nil, nil)
	defer svc.Close()
	ctx := context.Background()
	opp, err := store.CreateOpportunity(ctx, Opportunity{Title: "市场扫描", UserThesis: "测试筛选原因", Status: OpportunityStatusCompleted})
	if err != nil {
		t.Fatal(err)
	}
	discovery, _, err := store.CreateOpportunityDiscoveryRun(ctx, OpportunityDiscoveryRun{OpportunityID: opp.ID, Status: OpportunityDiscoveryRunStatusCompleted}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertOpportunityResult(ctx, OpportunityResult{RunID: discovery.ID, RawResult: map[string]any{
		"excluded": []any{map[string]any{"symbol": "600001", "reason": "与扫描主题没有直接映射"}},
	}}); err != nil {
		t.Fatal(err)
	}
	agentRun, _, err := store.CreateAgentRunWithLedger(ctx, AgentRun{
		TaskType: AgentTaskTypeStrategyGeneration, Status: AgentRunStatusCompleted,
	}, AgentDecisionLedger{TaskType: AgentTaskTypeStrategyGeneration, StructuredOutput: map[string]any{
		"result": map[string]any{"run_summary": map[string]any{"omitted_candidates": []any{
			map[string]any{"symbol": "600002", "reason": "缺少公司级强证据"},
		}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.CreateOpportunityMarketScanRun(ctx, OpportunityMarketScanRun{
		TriggerType: OpportunityMarketScanTriggerManual, Status: OpportunityMarketScanStatusCompleted,
		OpportunityID: opp.ID, DiscoveryRunID: discovery.ID, StrategyAgentRunID: agentRun.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertOpportunityMarketScanCandidates(ctx, []OpportunityMarketScanCandidate{
		{ScanRunID: run.ID, Symbol: "600001", Market: "SH", Name: "复核排除", Stage: OpportunityMarketScanCandidateReviewedOut, StrategyStatus: OpportunityMarketScanStrategySkipped},
		{ScanRunID: run.ID, Symbol: "600002", Market: "SH", Name: "策略省略", Stage: OpportunityMarketScanCandidateFinal, StrategyStatus: OpportunityMarketScanStrategySkipped},
	}); err != nil {
		t.Fatal(err)
	}
	items, err := svc.ListOpportunityMarketScanCandidates(ctx, OpportunityMarketScanCandidateListFilter{ScanRunID: run.ID, Limit: 10})
	if err != nil || len(items) != 2 {
		t.Fatalf("items=%+v err=%v", items, err)
	}
	bySymbol := map[string]string{}
	for _, item := range items {
		bySymbol[item.Symbol] = item.DecisionReason
	}
	if bySymbol["600001"] != "与扫描主题没有直接映射" || bySymbol["600002"] != "缺少公司级强证据" {
		t.Fatalf("decision reasons=%+v", bySymbol)
	}
}

func TestOpportunityMarketScanSchemaBackfillsTerminalResearchCandidates(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()
	ctx := context.Background()
	run, err := store.CreateOpportunityMarketScanRun(ctx, OpportunityMarketScanRun{TriggerType: OpportunityMarketScanTriggerManual, Status: OpportunityMarketScanStatusCompleted})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertOpportunityMarketScanCandidates(ctx, []OpportunityMarketScanCandidate{{ScanRunID: run.ID, Symbol: "600000", Market: "SH", Name: "测试", Stage: OpportunityMarketScanCandidateResearch}}); err != nil {
		t.Fatal(err)
	}
	if err := store.ensureOpportunityMarketScanSchema(ctx); err != nil {
		t.Fatal(err)
	}
	items, err := store.ListOpportunityMarketScanCandidates(ctx, OpportunityMarketScanCandidateListFilter{ScanRunID: run.ID, Stage: OpportunityMarketScanCandidateReviewedOut, Limit: 10})
	if err != nil || len(items) != 1 || items[0].ExclusionReason == "" {
		t.Fatalf("items=%+v err=%v", items, err)
	}
}

func TestOpportunityMarketScanDiscoveryRejectsSymbolOutsideBoundedSet(t *testing.T) {
	store := newTestStore(t)
	svc := NewService(store, nil, nil)
	defer svc.Close()
	ctx := context.Background()
	for _, instrument := range []StockV2Instrument{
		{ID: "allowed", Symbol: "600000", Market: "SH", InstrumentType: InstrumentTypeStock, Name: "允许标的"},
		{ID: "outside", Symbol: "000001", Market: "SZ", InstrumentType: InstrumentTypeStock, Name: "越界标的"},
	} {
		if err := store.UpsertInstrument(ctx, instrument); err != nil {
			t.Fatalf("upsert instrument: %v", err)
		}
	}
	opp, err := svc.CreateOpportunity(ctx, RequestCreateOpportunity{Title: "市场扫描", UserThesis: "验证候选边界", CreatedBy: OpportunityMarketScanCreatedBy})
	if err != nil {
		t.Fatalf("create opportunity: %v", err)
	}
	discovery, _, err := store.CreateOpportunityDiscoveryRun(ctx, OpportunityDiscoveryRun{OpportunityID: opp.ID}, nil)
	if err != nil {
		t.Fatalf("create discovery: %v", err)
	}
	scanRun, err := store.CreateOpportunityMarketScanRun(ctx, OpportunityMarketScanRun{
		TriggerType: OpportunityMarketScanTriggerManual, Status: OpportunityMarketScanStatusResearching,
		OpportunityID: opp.ID, DiscoveryRunID: discovery.ID,
	})
	if err != nil {
		t.Fatalf("create scan run: %v", err)
	}
	if err := store.UpsertOpportunityMarketScanCandidates(ctx, []OpportunityMarketScanCandidate{{
		ID: "allowed-scan", ScanRunID: scanRun.ID, Symbol: "600000", Market: "SH", Name: "允许标的",
		Stage: OpportunityMarketScanCandidateResearch,
	}}); err != nil {
		t.Fatalf("upsert allowed scan candidate: %v", err)
	}
	_, _, err = svc.opportunityDiscoveryReportFromResult(ctx, discovery, map[string]any{
		"schema_version": OpportunityDiscoveryReportSchemaVersion,
		"opportunity_id": opp.ID,
		"summary":        "越界测试",
		"candidates": []any{map[string]any{
			"symbol": "000001", "relevance_score": 80.0, "evidence_score": 70.0,
			"market_risk_score": 30.0, "confidence": .7, "horizon_outlooks": testModelHorizonOutlooks(10),
		}},
	})
	if !errors.Is(err, ErrInvalidOpportunityResult) {
		t.Fatalf("error=%v, want ErrInvalidOpportunityResult", err)
	}
}

func TestOpportunityMarketScanResearchFailureSchedulesDurableRetry(t *testing.T) {
	store := newTestStore(t)
	svc := NewService(store, nil, nil)
	defer svc.Close()
	ctx := context.Background()
	opp, err := svc.CreateOpportunity(ctx, RequestCreateOpportunity{Title: "市场扫描", UserThesis: "重试测试"})
	if err != nil {
		t.Fatalf("create opportunity: %v", err)
	}
	discovery, _, err := store.CreateOpportunityDiscoveryRun(ctx, OpportunityDiscoveryRun{
		OpportunityID: opp.ID, Status: OpportunityDiscoveryRunStatusFailed, ErrorMessage: "provider timeout",
	}, nil)
	if err != nil {
		t.Fatalf("create failed discovery: %v", err)
	}
	run, err := store.CreateOpportunityMarketScanRun(ctx, OpportunityMarketScanRun{
		TriggerType: OpportunityMarketScanTriggerManual, Status: OpportunityMarketScanStatusResearching,
		OpportunityID: opp.ID, DiscoveryRunID: discovery.ID,
	})
	if err != nil {
		t.Fatalf("create scan run: %v", err)
	}
	svc.advanceOpportunityMarketResearch(ctx, run)
	got, err := store.GetOpportunityMarketScanRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("get scan run: %v", err)
	}
	if got.Status != OpportunityMarketScanStatusResearching || got.RetryCount != 1 || got.NextRetryAt.IsZero() || got.ErrorMessage != discovery.ErrorMessage {
		t.Fatalf("retry state=%+v, want researching retry 1 with durable deadline", got)
	}
}

func TestOpportunityMarketScanStrategyBatchKeepsBoundedCandidateContext(t *testing.T) {
	candidateIDs := make([]string, 0, opportunityMarketScanStrategyLimit+2)
	for i := 0; i < opportunityMarketScanStrategyLimit+2; i++ {
		candidateIDs = append(candidateIDs, fmt.Sprintf("candidate-%02d", i))
	}
	input, err := normalizeStrategyGenerationInput(StrategyGenerationInput{
		Mode:          StrategyGenerationModeOpportunity,
		OpportunityID: "opp-1",
		CandidateID:   candidateIDs[0],
		CandidateIDs:  candidateIDs,
		TargetInstruments: []StrategyGenerationTargetInstrument{
			{Symbol: "600000", Market: "SH"},
			{Symbol: "000001", Market: "SZ"},
			{Symbol: "002230", Market: "SZ"},
		},
	})
	if err != nil {
		t.Fatalf("normalize opportunity strategy batch: %v", err)
	}
	if len(input.CandidateIDs) != opportunityMarketScanStrategyLimit || input.CandidateID != candidateIDs[0] {
		t.Fatalf("candidate context=%+v legacy=%q, want bounded batch with stable first candidate", input.CandidateIDs, input.CandidateID)
	}
	want := candidateIDs[:opportunityMarketScanStrategyLimit]
	parsed := strategyGenerationCandidateIDsFromTrigger(strategyGenerationTriggerID(input))
	if len(parsed) != len(want) {
		t.Fatalf("parsed candidate ids=%+v, want %+v", parsed, want)
	}
	for i := range want {
		if parsed[i] != want[i] {
			t.Fatalf("parsed candidate ids=%+v, want %+v", parsed, want)
		}
	}
	if err := validateStrategyGenerationDraftTargets(strategyGenerationTriggerID(input), []StrategyGenerationDraft{
		{DraftType: StrategyGenerationDraftTypeNewStrategy, Symbol: "600000"},
		{DraftType: StrategyGenerationDraftTypeNewStrategy, Symbol: "600999"},
	}); !errors.Is(err, ErrInvalidStrategyGenerationResult) {
		t.Fatalf("out-of-scan strategy target error=%v, want ErrInvalidStrategyGenerationResult", err)
	}
}

func TestOpportunityMarketStrategyRefreshPersistsExecutableQuote(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()
	ctx := context.Background()
	svc := NewService(store, nil, &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if strings.Contains(req.URL.Host, "qt.gtimg.cn") {
			return stringResponse(http.StatusOK, tencentQuoteLine("sh600000", "浦发银行", "600000", "12.34", "12.00")), nil
		}
		return stringResponse(http.StatusBadGateway, "unavailable"), nil
	})})
	defer svc.Close()

	svc.refreshOpportunityMarketStrategyQuotes(ctx, []OpportunityCandidate{{Symbol: "600000", Market: "SH", Name: "浦发银行"}})
	quotes, err := store.GetLatestQuotes(ctx, []string{"600000"})
	if err != nil {
		t.Fatalf("get refreshed strategy quote: %v", err)
	}
	if len(quotes) != 1 || quotes[0].LastPrice != 12.34 || quotes[0].Status != QuoteStatusFresh {
		t.Fatalf("quotes=%+v, want persisted fresh executable quote", quotes)
	}
}

func TestOpportunityMarketScanStrategyStepPromptKeepsAllBatchTargets(t *testing.T) {
	genCtx := StrategyGenerationContext{
		Input:                          StrategyGenerationInput{Mode: StrategyGenerationModeOpportunity},
		OpportunityEvidenceByCandidate: map[string][]OpportunityEvidence{},
	}
	for i := 0; i < opportunityMarketScanStrategyLimit; i++ {
		symbol := fmt.Sprintf("%06d", 600000+i)
		candidateID := "candidate-" + symbol
		genCtx.Input.CandidateIDs = append(genCtx.Input.CandidateIDs, candidateID)
		genCtx.Input.TargetInstruments = append(genCtx.Input.TargetInstruments, StrategyGenerationTargetInstrument{Symbol: symbol})
		genCtx.OpportunityCandidates = append(genCtx.OpportunityCandidates, OpportunityCandidate{
			ID: candidateID, Symbol: symbol, Name: "候选", Reason: strings.Repeat("候选理由", 100),
		})
		for evidenceIndex := 0; evidenceIndex < 10; evidenceIndex++ {
			genCtx.OpportunityEvidenceByCandidate[candidateID] = append(genCtx.OpportunityEvidenceByCandidate[candidateID], OpportunityEvidence{
				ID: candidateID + "-evidence", CandidateID: candidateID, SourceType: OpportunityEvidenceSourceExternal,
				Title: strings.Repeat("证据标题", 60), Summary: strings.Repeat("证据摘要", 100),
			})
		}
	}
	prompt := buildStrategyGenerationStepPrompt("task-batch", StrategyGenerationStepPack{
		RunID: "run-1", StepKey: StrategyGenerationStepFormatter, Role: "formatter",
		Objective: "生成有界策略草案", Context: genCtx,
	}, "http://127.0.0.1:8080/api/stockv2/agent/mcp")
	for _, expected := range []string{"600000", "600005", "600009", "candidate-600000", "candidate-600009"} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("batched strategy prompt missing %q", expected)
		}
	}
	if !strings.Contains(prompt, "intentionally may omit portfolioId") || !strings.Contains(prompt, "run_summary.mode must exactly equal context.input.mode") ||
		!strings.Contains(prompt, "decision_basis") || !strings.Contains(prompt, "context.decisionGates[symbol].id") {
		t.Fatalf("batched opportunity prompt missing research-scope or result-mode boundary")
	}
	if strings.Contains(prompt, promptTruncatedMarker) {
		t.Fatalf("bounded strategy prompt unexpectedly truncated at %d bytes", len(prompt))
	}
}

func TestStrategyGenerationPriorResultsKeepsOnlyDirectDependencies(t *testing.T) {
	all := map[string]any{
		StrategyGenerationStepEvidenceCollector: "evidence",
		StrategyGenerationStepBullResearcher:    "bull",
		StrategyGenerationStepBearResearcher:    "bear",
		StrategyGenerationStepEvidenceChecker:   "checked",
		StrategyGenerationStepPortfolioJudge:    "judged",
	}
	formatter := strategyGenerationPriorResults(StrategyGenerationStepFormatter, all)
	if len(formatter) != 2 || formatter[StrategyGenerationStepEvidenceChecker] != "checked" || formatter[StrategyGenerationStepPortfolioJudge] != "judged" {
		t.Fatalf("formatter dependencies=%+v", formatter)
	}
	if _, ok := formatter[StrategyGenerationStepEvidenceCollector]; ok {
		t.Fatalf("formatter prompt repeated non-direct evidence collector output")
	}
	checker := strategyGenerationPriorResults(StrategyGenerationStepEvidenceChecker, all)
	if len(checker) != 3 || checker[StrategyGenerationStepEvidenceCollector] != "evidence" || checker[StrategyGenerationStepBullResearcher] != "bull" || checker[StrategyGenerationStepBearResearcher] != "bear" {
		t.Fatalf("checker dependencies=%+v", checker)
	}
}
