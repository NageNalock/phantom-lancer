package stockv2

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"phantom-lancer/internal/safelog"
)

const (
	// ponytail: reference-data refresh is bounded to the already selected research
	// pool. If that pool grows beyond 20, move this to a scheduled batch import.
	decisionReferencePoolLimit = opportunityMarketScanResearchLimit
	decisionReferenceWorkers   = 4
	decisionReferenceTimeout   = 12 * time.Second
	decisionReferenceFreshness = 20 * time.Hour
	// ponytail: 200 rows keeps each request bounded while covering recent
	// quarterly disclosures and long dividend histories. If this becomes
	// insufficient, replace it with provider pagination rather than raising it.
	decisionReferenceRowLimit = 200
)

var decisionReferenceDatasets = []string{"disclosure_date", "share_float", "dividend", "forecast", "income", "cashflow", "fina_indicator"}

type decisionReferenceHealth struct {
	Symbol    string
	Status    string
	Source    string
	Message   string
	EventOK   bool
	FinanceOK bool
	CheckedAt time.Time
}

type tushareDatasetResult struct {
	Fields []string
	Items  [][]any
	Source string
}

func (s *Service) refreshDecisionReferenceData(ctx context.Context, config OpportunityMarketScanConfig, candidates []OpportunityMarketScanCandidate) map[string]decisionReferenceHealth {
	limit := min(len(candidates), decisionReferencePoolLimit)
	out := make(map[string]decisionReferenceHealth, limit)
	if limit == 0 {
		return out
	}
	type job struct{ symbol, market, instrumentType string }
	jobs := make(chan job)
	results := make(chan decisionReferenceHealth, limit)
	var wg sync.WaitGroup
	workerCount := min(decisionReferenceWorkers, limit)
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range jobs {
				results <- s.refreshOneDecisionReference(ctx, config, item.symbol, item.market, item.instrumentType)
			}
		}()
	}
	go func() {
		defer close(results)
		for i := 0; i < limit; i++ {
			select {
			case jobs <- job{candidates[i].Symbol, candidates[i].Market, candidates[i].Metrics.InstrumentType}:
			case <-ctx.Done():
				close(jobs)
				wg.Wait()
				return
			}
		}
		close(jobs)
		wg.Wait()
	}()
	for result := range results {
		out[result.Symbol] = result
	}
	return out
}

func (s *Service) refreshOneDecisionReference(ctx context.Context, config OpportunityMarketScanConfig, symbol, market string, instrumentTypes ...string) decisionReferenceHealth {
	now := time.Now()
	health := decisionReferenceHealth{Symbol: symbol, Status: DecisionHealthBlocked, CheckedAt: now}
	if cached, err := s.store.GetDecisionReferenceHealth(ctx, symbol); err == nil && cached.EventOK && now.Sub(cached.CheckedAt) < decisionReferenceFreshness {
		return cached
	}
	if !decisionSupportedMarket(market) {
		health.Message = "当前仅支持沪深股票与场内基金"
		return health
	}
	if len(instrumentTypes) > 0 && instrumentTypes[0] == InstrumentTypeExchangeFund {
		health.Status, health.EventOK, health.FinanceOK = DecisionHealthHealthy, true, true
		health.Message = "场内基金不适用公司事件与财务事实"
		return health
	}
	tsCode := decisionTSCode(symbol, market)
	if tsCode == "" {
		health.Message = "无法识别证券市场"
		return health
	}
	eventSuccess, financeSuccess := 0, 0
	var messages []string
	for _, dataset := range decisionReferenceDatasets {
		params := url.Values{"ts_code": {tsCode}, "limit": {strconv.Itoa(decisionReferenceRowLimit)}}
		result, err := s.fetchDecisionTushareDataset(ctx, config, dataset, params)
		if err != nil {
			if len(messages) < 3 {
				messages = append(messages, dataset+": "+safelog.Error(err, 100))
			}
			continue
		}
		if health.Source == "" {
			health.Source = result.Source
		} else if health.Source != result.Source {
			health.Source = "mixed"
		}
		if decisionEventDataset(dataset) {
			if err := s.store.UpsertDecisionMarketEvents(ctx, parseDecisionEvents(symbol, dataset, result, now)); err != nil {
				if len(messages) < 3 {
					messages = append(messages, dataset+": cache write failed")
				}
				continue
			}
			eventSuccess++
		} else {
			if err := s.store.UpsertDecisionFinancialFacts(ctx, parseDecisionFinancialFacts(symbol, dataset, result, now)); err != nil {
				if len(messages) < 3 {
					messages = append(messages, dataset+": cache write failed")
				}
				continue
			}
			financeSuccess++
		}
	}
	health.EventOK = eventSuccess == 4
	health.FinanceOK = financeSuccess == 3
	switch {
	case health.EventOK && health.FinanceOK:
		health.Status = DecisionHealthHealthy
	case health.EventOK:
		health.Status = DecisionHealthDegraded
		health.Message = "事件日历完整，财务事实源不完整"
	default:
		health.Status = DecisionHealthBlocked
		health.Message = "关键事件日历不完整"
	}
	if len(messages) > 0 {
		health.Message = strings.TrimSpace(strings.Join([]string{health.Message, strings.Join(messages, "; ")}, "；"))
	}
	_ = s.store.SaveDecisionReferenceHealth(ctx, health)
	return health
}

func (s *Service) fetchDecisionTushareDataset(ctx context.Context, config OpportunityMarketScanConfig, dataset string, params url.Values) (tushareDatasetResult, error) {
	if !decisionDatasetAllowed(dataset) {
		return tushareDatasetResult{}, errors.New("unsupported reference dataset")
	}
	sources := make([]opportunityFundFlowSource, 0, 2)
	if config.PrimaryFundFlowAPIKey != "" {
		sources = append(sources, opportunityFundFlowSource{
			Name: opportunityFundFlowSourcePrimary, Endpoint: decisionDatasetEndpoint(opportunityFundFlowPrimaryURL, dataset),
			Key: config.PrimaryFundFlowAPIKey, Client: opportunityFundFlowHTTPClient(nil),
		})
	}
	if config.BackupFundFlowAPIKey != "" {
		client, err := opportunityFundFlowBackupClient(config.BackupFundFlowProxy)
		if err != nil {
			return tushareDatasetResult{}, err
		}
		sources = append(sources, opportunityFundFlowSource{
			Name: opportunityFundFlowSourceBackup, Endpoint: decisionDatasetEndpoint(opportunityFundFlowBackupURL, dataset),
			Key: config.BackupFundFlowAPIKey, Client: client,
		})
	}
	if len(sources) == 0 {
		return tushareDatasetResult{}, errors.New("reference data credentials are not configured")
	}
	var failures []string
	for _, source := range sources {
		result, err := requestTushareDataset(ctx, source, params)
		if err == nil {
			return result, nil
		}
		failures = append(failures, source.Name+": "+safelog.Error(err, 100))
	}
	return tushareDatasetResult{}, errors.New(strings.Join(failures, "; "))
}

func requestTushareDataset(ctx context.Context, source opportunityFundFlowSource, params url.Values) (tushareDatasetResult, error) {
	requestCtx, cancel := context.WithTimeout(ctx, decisionReferenceTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, source.Endpoint+"?"+params.Encode(), nil)
	if err != nil {
		return tushareDatasetResult{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "phantom-lancer-stockv2/1.0")
	req.Header.Set("X-API-Key", source.Key)
	resp, err := source.Client.Do(req)
	if err != nil {
		return tushareDatasetResult{}, errors.New("network request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return tushareDatasetResult{}, fmt.Errorf("provider returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return tushareDatasetResult{}, err
	}
	var payload tushareRelayPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return tushareDatasetResult{}, errors.New("provider returned invalid JSON")
	}
	if payload.Code != 0 {
		return tushareDatasetResult{}, fmt.Errorf("provider business code %d: %s", payload.Code, safelog.Text(payload.Msg, 100))
	}
	return tushareDatasetResult{Fields: payload.Data.Fields, Items: payload.Data.Items, Source: source.Name}, nil
}

func decisionDatasetEndpoint(moneyFlowURL, dataset string) string {
	return strings.TrimSuffix(moneyFlowURL, "/moneyflow") + "/" + dataset
}

func decisionDatasetAllowed(dataset string) bool {
	if dataset == "index_daily" || dataset == "trade_cal" {
		return true
	}
	for _, item := range decisionReferenceDatasets {
		if item == dataset {
			return true
		}
	}
	return false
}

func (s *Service) refreshDecisionTradeCalendar(ctx context.Context, config OpportunityMarketScanConfig, centerDate string) ([]decisionTradeDay, error) {
	center, err := time.Parse("2006-01-02", centerDate)
	if err != nil {
		center = time.Now()
	}
	start := center.AddDate(0, 0, -40).Format("2006-01-02")
	end := center.AddDate(0, 0, 40).Format("2006-01-02")
	if cached, err := s.store.GetDecisionTradeCalendar(ctx, start, end); err == nil && len(cached) >= 60 &&
		time.Since(cached[len(cached)-1].FetchedAt) < decisionReferenceFreshness {
		return cached, nil
	}
	result, err := s.fetchDecisionTushareDataset(ctx, config, "trade_cal", url.Values{
		"exchange": {"SSE"}, "start_date": {strings.ReplaceAll(start, "-", "")},
		"end_date": {strings.ReplaceAll(end, "-", "")}, "limit": {"100"},
	})
	if err != nil {
		return nil, err
	}
	index := decisionFieldIndex(result.Fields)
	now := time.Now()
	items := make([]decisionTradeDay, 0, len(result.Items))
	for _, row := range result.Items {
		date := decisionDateValue(row, index, "cal_date")
		if date == "" {
			continue
		}
		items = append(items, decisionTradeDay{Date: date, PreviousDate: decisionDateValue(row, index, "pretrade_date"),
			Open: decisionNumberValue(row, index, "is_open") == 1, Source: result.Source, FetchedAt: now})
	}
	if len(items) == 0 {
		return nil, errors.New("trade_cal response has no valid rows")
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Date < items[j].Date })
	if err := s.store.UpsertDecisionTradeCalendar(ctx, items); err != nil {
		return nil, err
	}
	return items, nil
}

func (s *Service) refreshDecisionBenchmark(ctx context.Context, config OpportunityMarketScanConfig, endDate string) ([]decisionIndexBar, error) {
	if cached, err := s.store.GetDecisionIndexBars(ctx, "000300.SH", endDate, 260); err == nil && len(cached) >= 21 &&
		time.Since(cached[0].FetchedAt) < decisionReferenceFreshness {
		return cached, nil
	}
	end := strings.ReplaceAll(endDate, "-", "")
	parsed, err := time.Parse("20060102", end)
	if err != nil {
		parsed = time.Now()
		end = parsed.Format("20060102")
	}
	params := url.Values{
		"ts_code":    {"000300.SH"},
		"start_date": {parsed.AddDate(-1, 0, 0).Format("20060102")},
		"end_date":   {end},
		"limit":      {"260"},
	}
	result, err := s.fetchDecisionTushareDataset(ctx, config, "index_daily", params)
	if err != nil {
		return nil, err
	}
	index := decisionFieldIndex(result.Fields)
	now := time.Now()
	bars := make([]decisionIndexBar, 0, len(result.Items))
	for _, row := range result.Items {
		tradeDate := decisionDateValue(row, index, "trade_date")
		closeValue := decisionNumberValue(row, index, "close")
		if tradeDate == "" || closeValue <= 0 {
			continue
		}
		bars = append(bars, decisionIndexBar{Symbol: "000300.SH", TradeDate: tradeDate, Close: closeValue,
			PctChange: decisionNumberValue(row, index, "pct_chg"), Source: result.Source, FetchedAt: now})
	}
	if len(bars) == 0 {
		return nil, errors.New("index_daily response has no valid rows")
	}
	if err := s.store.UpsertDecisionIndexBars(ctx, bars); err != nil {
		return nil, err
	}
	return bars, nil
}

func (s *Service) ProbeOpportunityDecisionData(ctx context.Context) OpportunityDecisionDataProbe {
	now := time.Now()
	out := OpportunityDecisionDataProbe{CheckedAt: now, Sources: map[string]OpportunityDataSourceProbe{}}
	config, err := s.store.GetOpportunityMarketScanConfig(ctx)
	if err != nil {
		out.Status = DecisionHealthBlocked
		out.Sources["config"] = OpportunityDataSourceProbe{Status: DecisionHealthBlocked, Error: safelog.Error(err, 160)}
		return out
	}
	flow := s.ProbeOpportunityMarketFundFlow(ctx)
	out.Sources["fundFlow"] = OpportunityDataSourceProbe{Status: ternaryDecisionOptionalStatus(flow.OK), Source: flow.Source, Count: flow.Count, Error: flow.Error}
	benchmark, benchmarkErr := s.refreshDecisionBenchmark(ctx, config, now.Format("2006-01-02"))
	out.Sources["benchmark"] = OpportunityDataSourceProbe{Status: ternaryDecisionStatus(benchmarkErr == nil), Count: len(benchmark)}
	if len(benchmark) > 0 {
		out.Sources["benchmark"] = OpportunityDataSourceProbe{Status: DecisionHealthHealthy, Source: benchmark[0].Source, Count: len(benchmark)}
	}
	if benchmarkErr != nil {
		item := out.Sources["benchmark"]
		item.Error = safelog.Error(benchmarkErr, 160)
		out.Sources["benchmark"] = item
	}
	tradeCalendar, tradeCalendarErr := s.refreshDecisionTradeCalendar(ctx, config, now.Format("2006-01-02"))
	out.Sources["tradeCalendar"] = OpportunityDataSourceProbe{Status: ternaryDecisionStatus(tradeCalendarErr == nil), Count: len(tradeCalendar)}
	if len(tradeCalendar) > 0 {
		item := out.Sources["tradeCalendar"]
		item.Source = tradeCalendar[0].Source
		out.Sources["tradeCalendar"] = item
	}
	if tradeCalendarErr != nil {
		item := out.Sources["tradeCalendar"]
		item.Error = safelog.Error(tradeCalendarErr, 160)
		out.Sources["tradeCalendar"] = item
	}
	reference := s.refreshOneDecisionReference(ctx, config, "000001", "SZ")
	out.Sources["eventCalendar"] = OpportunityDataSourceProbe{Status: ternaryDecisionStatus(reference.EventOK), Source: reference.Source}
	out.Sources["financialFacts"] = OpportunityDataSourceProbe{Status: ternaryDecisionOptionalStatus(reference.FinanceOK), Source: reference.Source}
	if reference.Message != "" {
		if !reference.EventOK {
			item := out.Sources["eventCalendar"]
			item.Error = safelog.Text(reference.Message, 160)
			out.Sources["eventCalendar"] = item
		}
		if !reference.FinanceOK {
			item := out.Sources["financialFacts"]
			item.Error = safelog.Text(reference.Message, 160)
			out.Sources["financialFacts"] = item
		}
	}
	out.OK = benchmarkErr == nil && tradeCalendarErr == nil && reference.EventOK
	out.Status = DecisionHealthHealthy
	if !out.OK {
		out.Status = DecisionHealthBlocked
	} else if !flow.OK || !reference.FinanceOK {
		out.Status = DecisionHealthDegraded
	}
	return out
}

func ternaryDecisionStatus(ok bool) string {
	if ok {
		return DecisionHealthHealthy
	}
	return DecisionHealthBlocked
}

func ternaryDecisionOptionalStatus(ok bool) string {
	if ok {
		return DecisionHealthHealthy
	}
	return DecisionHealthDegraded
}

func decisionEventDataset(dataset string) bool {
	switch dataset {
	case "disclosure_date", "share_float", "dividend", "forecast":
		return true
	default:
		return false
	}
}

func decisionTSCode(symbol, market string) string {
	market = strings.ToUpper(strings.TrimSpace(market))
	if market != "SH" && market != "SZ" {
		return ""
	}
	return strings.TrimSpace(symbol) + "." + market
}

func decisionSupportedMarket(market string) bool {
	market = strings.ToUpper(strings.TrimSpace(market))
	return market == "SH" || market == "SZ"
}

func parseDecisionEvents(symbol, dataset string, result tushareDatasetResult, fetchedAt time.Time) []decisionMarketEvent {
	index := decisionFieldIndex(result.Fields)
	out := make([]decisionMarketEvent, 0, len(result.Items))
	for _, row := range result.Items {
		ann := decisionDateValue(row, index, "ann_date")
		eventDate, title := "", ""
		switch dataset {
		case "disclosure_date":
			eventDate = firstNonEmpty(decisionDateValue(row, index, "actual_date"), decisionDateValue(row, index, "pre_date"))
			title = "定期报告披露"
		case "share_float":
			eventDate, title = decisionDateValue(row, index, "float_date"), "限售股份解禁"
		case "dividend":
			eventDate = firstNonEmpty(decisionDateValue(row, index, "ex_date"), decisionDateValue(row, index, "pay_date"), ann)
			title = "分红除权"
		case "forecast":
			eventDate = ann
			title = "业绩预告"
		}
		if eventDate == "" {
			continue
		}
		out = append(out, decisionMarketEvent{Symbol: symbol, EventType: dataset, EventDate: eventDate,
			AnnouncedAt: ann, Title: title, Source: result.Source, FetchedAt: fetchedAt})
	}
	return out
}

func parseDecisionFinancialFacts(symbol, dataset string, result tushareDatasetResult, fetchedAt time.Time) []decisionFinancialFact {
	index := decisionFieldIndex(result.Fields)
	out := make([]decisionFinancialFact, 0, len(result.Items))
	for _, row := range result.Items {
		period := decisionDateValue(row, index, "end_date")
		if period == "" {
			continue
		}
		item := decisionFinancialFact{Symbol: symbol, ReportPeriod: period, Dataset: dataset,
			AnnouncedAt: decisionDateValue(row, index, "ann_date"), Source: result.Source, FetchedAt: fetchedAt}
		switch dataset {
		case "income":
			item.Revenue = decisionNumberValue(row, index, "revenue")
			item.NetProfit = firstNonZero(decisionNumberValue(row, index, "n_income_attr_p"), decisionNumberValue(row, index, "n_income"))
		case "cashflow":
			item.OperatingCashFlow = decisionNumberValue(row, index, "n_cashflow_act")
		case "fina_indicator":
			item.ROE = firstNonZero(decisionNumberValue(row, index, "roe"), decisionNumberValue(row, index, "roe_waa"))
			item.GrossMargin = decisionNumberValue(row, index, "grossprofit_margin")
		}
		out = append(out, item)
	}
	return out
}

func decisionFieldIndex(fields []string) map[string]int {
	out := make(map[string]int, len(fields))
	for i, field := range fields {
		out[field] = i
	}
	return out
}

func decisionStringValue(row []any, index map[string]int, field string) string {
	i, ok := index[field]
	if !ok || i < 0 || i >= len(row) || row[i] == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(row[i]))
}

func decisionDateValue(row []any, index map[string]int, field string) string {
	value := strings.ReplaceAll(decisionStringValue(row, index, field), "-", "")
	if len(value) != 8 {
		return ""
	}
	if _, err := strconv.Atoi(value); err != nil {
		return ""
	}
	return value[:4] + "-" + value[4:6] + "-" + value[6:]
}

func decisionNumberValue(row []any, index map[string]int, field string) float64 {
	i, ok := index[field]
	if !ok || i < 0 || i >= len(row) {
		return 0
	}
	return floatFromAny(row[i])
}

func firstNonZero(values ...float64) float64 {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}
