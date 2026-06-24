package stock

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"phantom-lancer/internal/storage"
)

const (
	stockAutoMarketSource  = "local_data_scheduler"
	stockOpportunitySource = "local_opportunity_discovery"
)

func (s *Service) RunDataMaintenance(ctx context.Context, requestedBy string) (DataMaintenanceResult, error) {
	requestedBy = defaultString(requestedBy, "system")
	started := s.now()
	s.log.InfoContext(ctx, "stock: data maintenance started",
		"requested_by", requestedBy,
	)
	result := DataMaintenanceResult{}
	universe, err := s.RefreshAStockUniverse(ctx, MaintenanceModeDaily, requestedBy)
	if err != nil {
		s.log.WarnContext(ctx, "stock: data maintenance universe refresh failed",
			"error", err.Error(),
		)
		result.Notes = append(result.Notes, "A 股主数据刷新失败: "+err.Error())
	} else {
		if universe.Task.ID != "" {
			result.Tasks = append(result.Tasks, universe.Task)
			result.Instruments = append(result.Instruments, universe.Instruments...)
		}
		result.Notes = append(result.Notes, universe.Notes...)
	}
	health, err := s.RunDataSourceHealthCheck(ctx, "")
	if err != nil {
		s.log.WarnContext(ctx, "stock: data maintenance health check failed",
			"error", err.Error(),
		)
		return result, err
	}
	result.Tasks = append(result.Tasks, health.Task)
	result.Sources = append(result.Sources, health.Sources...)

	quotes, err := s.RecordQuoteRefreshStatus(ctx, requestedBy)
	if err == nil {
		result.Tasks = append(result.Tasks, quotes.Task)
		result.Quotes = append(result.Quotes, quotes.Quotes...)
		result.Notes = append(result.Notes, quotes.Notes...)
	} else {
		s.log.WarnContext(ctx, "stock: data maintenance quote refresh failed",
			"error", err.Error(),
		)
		result.Notes = append(result.Notes, "行情刷新失败: "+err.Error())
	}

	market, err := s.CollectMarketDataFromQuotes(ctx, requestedBy)
	if err != nil {
		s.log.WarnContext(ctx, "stock: data maintenance market data collection failed",
			"error", err.Error(),
		)
		return result, err
	}
	result.Tasks = append(result.Tasks, market.Task)
	result.MarketData = append(result.MarketData, market.MarketData...)
	result.Notes = append(result.Notes, market.Notes...)

	for _, taskResult := range []func(context.Context, string) (DataTaskResult, error){
		s.CollectNewsFeed,
		s.CollectFinancialReports,
		s.CollectResearchReports,
	} {
		collected, err := taskResult(ctx, requestedBy)
		if err != nil {
			s.log.WarnContext(ctx, "stock: data maintenance feed collection failed",
				"error", err.Error(),
			)
			return result, err
		}
		result.Tasks = append(result.Tasks, collected.Task)
		result.NewsItems = append(result.NewsItems, collected.NewsItems...)
		result.Alerts = append(result.Alerts, collected.Alerts...)
		result.Notes = append(result.Notes, collected.Notes...)
	}

	discovered, err := s.DiscoverOpportunities(ctx, requestedBy)
	if err != nil {
		s.log.WarnContext(ctx, "stock: data maintenance opportunity discovery failed",
			"error", err.Error(),
		)
		return result, err
	}
	result.Tasks = append(result.Tasks, discovered.Task)
	result.Opportunities = append(result.Opportunities, discovered.Opportunities...)
	result.Notes = append(result.Notes, discovered.Notes...)
	s.log.InfoContext(ctx, "stock: data maintenance completed",
		"tasks", len(result.Tasks),
		"instruments", len(result.Instruments),
		"quotes", len(result.Quotes),
		"opportunities", len(result.Opportunities),
		"latency_ms", s.now().Sub(started).Milliseconds(),
	)
	return result, nil
}

func (s *Service) CollectMarketDataFromQuotes(ctx context.Context, requestedBy string) (DataTaskResult, error) {
	src, ready, reason, next, err := s.governedDataSourceReady(ctx, stockAutoMarketSource, "scheduler")
	if err != nil {
		return DataTaskResult{}, err
	}
	if !ready {
		return s.recordBlockedDataTask(ctx, "market_data_collection", src.Source, "", requestedBy, reason, next)
	}
	quotes, err := s.store.ListStockQuotes(ctx, 240)
	if err != nil {
		return DataTaskResult{}, err
	}
	if len(quotes) == 0 {
		return s.recordBlockedDataTask(ctx, "market_data_collection", src.Source, "", requestedBy, "没有可落盘的行情快照", time.Time{})
	}
	var points []storage.StockMarketDataPoint
	dataDate := s.marketDataDate()
	for _, quote := range quotes {
		if strings.TrimSpace(quote.Symbol) == "" || quote.LastPrice <= 0 {
			continue
		}
		open := quote.PreviousClose
		if open <= 0 {
			open = quote.LastPrice
		}
		quality := "fresh"
		if !quoteUsableForOperation(quote, s.now(), 30*time.Minute) {
			quality = "stale"
		}
		raw := mustJSON(map[string]any{
			"derived_from":    "stock_quotes",
			"data_timestamp":  quote.DataTimestamp,
			"freshness":       quote.DataFreshness,
			"tradable_status": quote.TradableStatus,
		})
		points = append(points,
			storage.StockMarketDataPoint{
				Symbol: quote.Symbol, Market: quote.Market, Dataset: "quote_derived_kline", DataDate: dataDate,
				Open: open, High: math.Max(open, quote.LastPrice), Low: math.Min(open, quote.LastPrice), Close: quote.LastPrice,
				Volume: quote.Volume, Amount: quote.Amount, Quality: quality, RawJSON: raw,
			},
			storage.StockMarketDataPoint{
				Symbol: quote.Symbol, Market: quote.Market, Dataset: "quote_snapshot", DataDate: dataDate,
				Open: quote.LastPrice, High: quote.LastPrice, Low: quote.LastPrice, Close: quote.LastPrice,
				Volume: quote.Volume, Amount: quote.Amount, Quality: quality, RawJSON: raw,
			},
		)
	}
	if len(points) == 0 {
		return s.recordBlockedDataTask(ctx, "market_data_collection", src.Source, "", requestedBy, "行情快照缺少价格，无法落盘为派生快照", time.Time{})
	}
	var saved []storage.StockMarketDataPoint
	var notes []string
	failed := 0
	for _, point := range points {
		point.Source = src.Source
		created, _, err := s.store.UpsertStockMarketDataPoint(ctx, point)
		if err != nil {
			failed++
			notes = append(notes, err.Error())
			continue
		}
		saved = append(saved, created)
	}
	status := taskStatus(len(saved), failed)
	task, err := s.store.CreateStockDataTask(ctx, storage.StockDataTask{
		TaskType:       "market_data_collection",
		Source:         src.Source,
		Status:         status,
		RequestedBy:    defaultString(requestedBy, "system"),
		InputJSON:      mustJSON(map[string]any{"count": len(points), "source": src.Source, "mode": "quote_snapshot_derivation"}),
		ResultJSON:     mustJSON(map[string]any{"saved": len(saved), "failed": failed}),
		ProcessedCount: len(saved),
		FailedCount:    failed,
		FailureSummary: strings.Join(notes, "; "),
	})
	if err != nil {
		_, _ = s.recordDataSourceFailure(ctx, src, err.Error())
		return DataTaskResult{}, err
	}
	_, _ = s.recordDataSourceSuccess(ctx, src, task.CompletedAt, task.Status, notes)
	notes = append(notes, "已从最新行情快照生成 quote_derived_kline 与 quote_snapshot 数据点")
	return DataTaskResult{Task: task, MarketData: saved, Notes: notes}, nil
}

func (s *Service) CollectNewsFeed(ctx context.Context, requestedBy string) (DataTaskResult, error) {
	return s.collectHTTPNewsLikeFeed(ctx, "news_collection", "jin10_search", "search", "PL_STOCK_JIN10_SEARCH_URL", "news", requestedBy, "新闻/金十搜索源需要配置用户自有 HTTP JSON adapter，或由 skill 写入 /api/stock/news/ingest 后参与机会发现")
}

func (s *Service) CollectFinancialReports(ctx context.Context, requestedBy string) (DataTaskResult, error) {
	return s.collectHTTPNewsLikeFeed(ctx, "financial_report_collection", "financial_report_feed", "report", "PL_STOCK_FINANCIAL_REPORT_URL", "financial_report", requestedBy, "财报/公告源需要配置用户自有 HTTP JSON adapter，或由 skill 写入 category=financial_report 的消息后参与机会发现")
}

func (s *Service) CollectResearchReports(ctx context.Context, requestedBy string) (DataTaskResult, error) {
	return s.collectHTTPNewsLikeFeed(ctx, "research_report_collection", "research_report_feed", "report", "PL_STOCK_RESEARCH_REPORT_URL", "research_report", requestedBy, "研报源需要配置用户自有 HTTP JSON adapter，或由 skill 写入 category=research_report 的消息后参与机会发现")
}

func (s *Service) collectHTTPNewsLikeFeed(ctx context.Context, taskType, source, sourceType, envKey, category, requestedBy, missingAdapterReason string) (DataTaskResult, error) {
	endpoint := strings.TrimSpace(os.Getenv(envKey))
	if endpoint == "" {
		s.log.DebugContext(ctx, "stock: feed adapter not configured",
			"task_type", taskType,
			"source", source,
			"env_key", envKey,
		)
		return s.recordAdapterBackedCollection(ctx, taskType, source, sourceType, requestedBy, missingAdapterReason)
	}
	src, ready, reason, next, err := s.governedDataSourceReady(ctx, source, sourceType)
	if err != nil {
		return DataTaskResult{}, err
	}
	if !ready {
		return s.recordBlockedDataTask(ctx, taskType, src.Source, "", requestedBy, reason, next)
	}
	queries, err := s.discoveryQueries(ctx)
	if err != nil {
		return DataTaskResult{}, err
	}
	if len(queries) == 0 {
		return s.recordBlockedDataTask(ctx, taskType, src.Source, "", requestedBy, "没有可用于周期抓取的股票代码或名称", time.Time{})
	}
	s.log.DebugContext(ctx, "stock: feed collection started",
		"task_type", taskType,
		"source", source,
		"queries", len(queries),
	)
	var fetched []storage.StockNewsItem
	var notes []string
	failed := 0
	for _, query := range queries {
		items, err := s.fetchHTTPNewsLikeFeed(ctx, endpoint, query.Query, category, query)
		if err != nil {
			s.log.DebugContext(ctx, "stock: feed query failed",
				"task_type", taskType,
				"source", source,
				"query", query.Symbol,
				"error", err.Error(),
			)
			failed++
			notes = append(notes, stockAdapterErrorSummary(err))
			continue
		}
		fetched = append(fetched, items...)
	}
	if failed > 0 && len(fetched) == 0 {
		s.log.WarnContext(ctx, "stock: feed collection all queries failed",
			"task_type", taskType,
			"source", source,
			"queries", len(queries),
			"error", strings.Join(notes, "; "),
		)
		task, err := s.store.CreateStockDataTask(ctx, storage.StockDataTask{
			TaskType:       taskType,
			Source:         src.Source,
			Status:         "failed",
			RequestedBy:    defaultString(requestedBy, "system"),
			InputJSON:      mustJSON(map[string]any{"queries": len(queries), "adapter": envKey}),
			ResultJSON:     mustJSON(map[string]any{"fetched": 0, "failed": failed}),
			FailedCount:    failed,
			FailureSummary: strings.Join(notes, "; "),
			NextRunAt:      s.now().UTC().Add(quoteProviderBackoff(src.RateLimitSeconds, src.ConsecutiveFailures+1)).Format(time.RFC3339Nano),
		})
		if err != nil {
			return DataTaskResult{}, err
		}
		return DataTaskResult{Task: task, Notes: notes}, nil
	}
	var saved []storage.StockNewsItem
	var created []storage.StockNewsItem
	lastCursor := src.LastCursor
	for _, item := range fetched {
		item.Source = src.Source
		item.Title = limitText(item.Title, 240)
		item.Summary = limitText(item.Summary, 1200)
		item.RawPayload = ""
		if item.PublishedAt == "" {
			item.PublishedAt = s.now().Format(time.RFC3339Nano)
		}
		stored, isNew, err := s.store.UpsertStockNewsItem(ctx, item)
		if err != nil {
			failed++
			notes = append(notes, stockAdapterErrorSummary(err))
			continue
		}
		saved = append(saved, stored)
		if isNew {
			created = append(created, stored)
		}
		if stored.PublishedAt > lastCursor {
			lastCursor = stored.PublishedAt
		}
	}
	alerts, err := s.createNewsAlerts(ctx, created)
	if err != nil {
		return DataTaskResult{}, err
	}
	status := taskStatus(len(saved), failed)
	if len(saved) == 0 && failed == 0 {
		notes = append(notes, "adapter 本轮没有返回可落盘条目")
	}
	task, err := s.store.CreateStockDataTask(ctx, storage.StockDataTask{
		TaskType:       taskType,
		Source:         src.Source,
		Status:         status,
		RequestedBy:    defaultString(requestedBy, "system"),
		InputJSON:      mustJSON(map[string]any{"queries": len(queries), "adapter": envKey}),
		ResultJSON:     mustJSON(map[string]any{"fetched": len(fetched), "saved": len(saved), "new": len(created), "alerts": len(alerts), "failed": failed}),
		ProcessedCount: len(saved),
		FailedCount:    failed,
		FailureSummary: strings.Join(notes, "; "),
	})
	if err != nil {
		return DataTaskResult{}, err
	}
	if failed == 0 {
		base := s.now().UTC()
		if parsed, ok := parseStockTime(task.CompletedAt); ok {
			base = parsed.UTC()
		}
		_, _ = s.store.UpdateStockDataSourceHealth(ctx, storage.StockDataSource{
			Source:              src.Source,
			Status:              "available",
			Quality:             mapQuoteRefreshQuality(task.Status),
			LastCursor:          lastCursor,
			LastIngestedAt:      task.CompletedAt,
			NextAllowedAt:       base.Add(time.Duration(maxInt(src.RateLimitSeconds, 60)) * time.Second).Format(time.RFC3339Nano),
			ConsecutiveFailures: 0,
			FailureSummary:      limitText(strings.Join(notes, "; "), stockQuoteFailureSummaryMaxLength),
		})
	} else {
		_, _ = s.recordDataSourceFailure(ctx, src, strings.Join(notes, "; "))
	}
	return DataTaskResult{Task: task, NewsItems: saved, Alerts: alerts, Notes: notes}, nil
}

func (s *Service) recordAdapterBackedCollection(ctx context.Context, taskType, source, sourceType, requestedBy, missingAdapterReason string) (DataTaskResult, error) {
	src, ready, reason, next, err := s.governedDataSourceReady(ctx, source, sourceType)
	if err != nil {
		return DataTaskResult{}, err
	}
	if !ready {
		return s.recordBlockedDataTask(ctx, taskType, src.Source, "", requestedBy, reason, next)
	}
	nextRun := s.now().UTC().Add(time.Duration(maxInt(src.RateLimitSeconds, 60)) * time.Second)
	_, _ = s.store.UpdateStockDataSourceHealth(ctx, storage.StockDataSource{
		Source:              src.Source,
		Status:              "degraded",
		Quality:             "partial",
		NextAllowedAt:       nextRun.Format(time.RFC3339Nano),
		ConsecutiveFailures: src.ConsecutiveFailures + 1,
		FailureSummary:      limitText(missingAdapterReason, stockQuoteFailureSummaryMaxLength),
	})
	return s.recordBlockedDataTask(ctx, taskType, src.Source, "", requestedBy, missingAdapterReason, nextRun)
}

func (s *Service) DiscoverOpportunities(ctx context.Context, requestedBy string) (DataTaskResult, error) {
	created := []storage.StockOpportunity{}
	notes := []string{}
	newsItems, err := s.store.ListStockNewsItems(ctx, "", "", 240)
	if err != nil {
		return DataTaskResult{}, err
	}
	for _, item := range newsItems {
		if !newsItemSuggestsOpportunity(item) {
			continue
		}
		op, ok, err := s.createOpportunityIfMissing(ctx, opportunityFromNews(item))
		if err != nil {
			return DataTaskResult{}, err
		}
		if ok {
			created = append(created, op)
		}
	}
	quotes, err := s.store.ListStockQuotes(ctx, 240)
	if err != nil {
		return DataTaskResult{}, err
	}
	for _, quote := range quotes {
		op, ok, err := s.opportunityFromQuoteMove(ctx, quote)
		if err != nil {
			return DataTaskResult{}, err
		}
		if ok {
			created = append(created, op)
		}
	}
	points, err := s.store.ListStockMarketDataPoints(ctx, "", "daily_kline", 240)
	if err != nil {
		return DataTaskResult{}, err
	}
	for _, point := range points {
		op, ok, err := s.opportunityFromKLine(ctx, point)
		if err != nil {
			return DataTaskResult{}, err
		}
		if ok {
			created = append(created, op)
		}
	}
	status := "completed"
	if len(created) == 0 {
		notes = append(notes, "本轮没有发现新的去重机会")
	}
	task, err := s.store.CreateStockDataTask(ctx, storage.StockDataTask{
		TaskType:       "opportunity_discovery",
		Source:         stockOpportunitySource,
		Status:         status,
		RequestedBy:    defaultString(requestedBy, "system"),
		InputJSON:      mustJSON(map[string]any{"news": len(newsItems), "quotes": len(quotes), "kline": len(points)}),
		ResultJSON:     mustJSON(map[string]any{"created": len(created), "notes": notes}),
		ProcessedCount: len(newsItems) + len(quotes) + len(points),
		FailureSummary: strings.Join(notes, "; "),
	})
	if err != nil {
		return DataTaskResult{}, err
	}
	s.log.DebugContext(ctx, "stock: opportunity discovery finished",
		"created", len(created),
		"news_items", len(newsItems),
		"quotes", len(quotes),
		"kline_points", len(points),
	)
	return DataTaskResult{Task: task, Opportunities: created, Notes: notes}, nil
}

type discoveryQuery struct {
	Symbol string
	Market string
	Name   string
	Query  string
}

type StockDataAdapterStatus struct {
	Key        string `json:"key"`
	Source     string `json:"source"`
	Label      string `json:"label"`
	Category   string `json:"category"`
	Configured bool   `json:"configured"`
}

func StockDataAdapterStatuses() []StockDataAdapterStatus {
	adapters := []StockDataAdapterStatus{
		{Key: "PL_STOCK_JIN10_SEARCH_URL", Source: "jin10_search", Label: "新闻 / 金十搜索 adapter", Category: "news"},
		{Key: "PL_STOCK_FINANCIAL_REPORT_URL", Source: "financial_report_feed", Label: "财报 / 公告 adapter", Category: "financial_report"},
		{Key: "PL_STOCK_RESEARCH_REPORT_URL", Source: "research_report_feed", Label: "研报 adapter", Category: "research_report"},
	}
	for i := range adapters {
		adapters[i].Configured = strings.TrimSpace(os.Getenv(adapters[i].Key)) != ""
	}
	return adapters
}

func (s *Service) discoveryQueries(ctx context.Context) ([]discoveryQuery, error) {
	seen := map[string]bool{}
	var queries []discoveryQuery
	add := func(symbol, market, name string) {
		symbol = strings.TrimSpace(symbol)
		if symbol == "" || seen[symbol] {
			return
		}
		seen[symbol] = true
		query := symbol
		if strings.TrimSpace(name) != "" {
			query += " " + strings.TrimSpace(name)
		}
		queries = append(queries, discoveryQuery{Symbol: symbol, Market: market, Name: name, Query: query})
	}
	watches, err := s.store.ListActiveStockWatches(ctx)
	if err != nil {
		return nil, err
	}
	for _, watch := range watches {
		add(watch.Symbol, watch.Market, watch.Name)
	}
	quotes, err := s.store.ListStockQuotes(ctx, 80)
	if err != nil {
		return nil, err
	}
	for _, quote := range quotes {
		add(quote.Symbol, quote.Market, quote.Name)
	}
	if len(queries) < 8 {
		instruments, _, err := s.QueryUniverse(ctx, UniverseQuery{PageSize: 80, SortBy: SortUpdatedDesc})
		if err != nil {
			return nil, err
		}
		for _, instrument := range instruments {
			add(instrument.Symbol, instrument.Market, instrument.Name)
			if len(queries) >= 8 {
				break
			}
		}
	}
	if len(queries) > 8 {
		queries = queries[:8]
	}
	return queries, nil
}

func (s *Service) fetchHTTPNewsLikeFeed(ctx context.Context, endpoint, query, category string, ref discoveryQuery) ([]storage.StockNewsItem, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	values := u.Query()
	if values.Get("q") == "" && values.Get("query") == "" {
		values.Set("q", query)
	}
	if values.Get("page_size") == "" && values.Get("limit") == "" {
		values.Set("page_size", "10")
	}
	u.RawQuery = values.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", stockQuoteUserAgent)
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s status %d", ref.Symbol, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		return nil, err
	}
	var payload any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	objects := extractFeedObjects(payload)
	items := make([]storage.StockNewsItem, 0, len(objects))
	for _, object := range objects {
		title := firstObjectString(object, "title", "headline", "name")
		if title == "" {
			continue
		}
		summary := firstObjectString(object, "summary", "snippet", "content", "description", "body")
		sourceID := firstObjectString(object, "id", "sourceItemId", "uuid")
		if sourceID == "" {
			sourceID = title
		}
		publishedAt := normalizeFeedTime(firstObjectString(object, "publishedAt", "published_at", "time", "datetime", "date"))
		importance := "normal"
		if looksImportant(title + " " + summary) {
			importance = "high"
		}
		items = append(items, storage.StockNewsItem{
			SourceItemID: limitText(sourceID, 240),
			Symbol:       ref.Symbol,
			Market:       ref.Market,
			Title:        title,
			Summary:      summary,
			Category:     category,
			Importance:   importance,
			Keywords:     strings.TrimSpace(ref.Symbol + " " + ref.Name + " " + category),
			Quality:      "fresh",
			PublishedAt:  publishedAt,
		})
	}
	return items, nil
}

func extractFeedObjects(value any) []map[string]any {
	switch typed := value.(type) {
	case []any:
		var out []map[string]any
		for _, item := range typed {
			if object, ok := item.(map[string]any); ok {
				out = append(out, object)
			}
		}
		return out
	case map[string]any:
		for _, key := range []string{"items", "results", "data", "list", "records"} {
			if items := extractFeedObjects(typed[key]); len(items) > 0 {
				return items
			}
		}
		if nested, ok := typed["data"].(map[string]any); ok {
			return extractFeedObjects(nested)
		}
	}
	return nil
}

func firstObjectString(object map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := object[key]; ok {
			switch typed := value.(type) {
			case string:
				if strings.TrimSpace(typed) != "" {
					return strings.TrimSpace(typed)
				}
			case float64:
				if typed > 0 {
					return fmt.Sprintf("%.0f", typed)
				}
			}
		}
	}
	return ""
}

func normalizeFeedTime(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.Format(time.RFC3339Nano)
		}
	}
	loc := time.FixedZone("Asia/Shanghai", 8*3600)
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02"} {
		if parsed, err := time.ParseInLocation(layout, value, loc); err == nil {
			return parsed.Format(time.RFC3339Nano)
		}
	}
	return ""
}

func looksImportant(value string) bool {
	value = strings.ToLower(value)
	for _, marker := range []string{"突发", "重要", "公告", "业绩", "中标", "回购", "并购", "urgent", "important", "breaking"} {
		if strings.Contains(value, strings.ToLower(marker)) {
			return true
		}
	}
	return false
}

func stockAdapterErrorSummary(err error) string {
	if err == nil {
		return ""
	}
	parts := strings.Fields(err.Error())
	for i, part := range parts {
		candidate := strings.Trim(part, "\"':,;()[]{}")
		if !strings.Contains(candidate, "://") {
			continue
		}
		if parsed, parseErr := url.Parse(candidate); parseErr == nil && parsed.Scheme != "" && parsed.Host != "" {
			parsed.RawQuery = ""
			parsed.Fragment = ""
			parts[i] = strings.Replace(part, candidate, parsed.String(), 1)
		}
	}
	return limitText(strings.Join(parts, " "), stockQuoteFailureSummaryMaxLength)
}

func (s *Service) createOpportunityIfMissing(ctx context.Context, op storage.StockOpportunity) (storage.StockOpportunity, bool, error) {
	if strings.TrimSpace(op.SourceType) != "" && strings.TrimSpace(op.SourceRefID) != "" {
		if existing, err := s.store.GetStockOpportunityBySource(ctx, op.SourceType, op.SourceRefID); err == nil {
			return existing, false, nil
		} else if !errors.Is(err, storage.ErrNotFound) {
			return storage.StockOpportunity{}, false, err
		}
	}
	created, err := s.store.CreateStockOpportunity(ctx, op)
	if err != nil {
		return storage.StockOpportunity{}, false, err
	}
	_, _ = s.store.CreateStockMemory(ctx, storage.StockMemory{
		Symbol:     created.Symbol,
		ObjectType: "opportunity",
		ObjectID:   created.ID,
		Summary:    fmt.Sprintf("自动发现机会: %s / %s", created.Symbol, created.Title),
	})
	return created, true, nil
}

func opportunityFromNews(item storage.StockNewsItem) storage.StockOpportunity {
	category := strings.ToLower(strings.TrimSpace(item.Category))
	theme := "消息面"
	if category == "financial_report" || category == "earnings" || category == "announcement" {
		theme = "财报公告"
	} else if category == "research_report" || category == "research" {
		theme = "研报观点"
	} else if category == "policy" {
		theme = "政策催化"
	}
	confidence := "medium"
	if item.Importance == "urgent" || item.Importance == "high" {
		confidence = "high"
	}
	return storage.StockOpportunity{
		Title:           fmt.Sprintf("%s %s机会: %s", item.Symbol, theme, item.Title),
		SourceType:      "news_item",
		SourceRefID:     item.ID,
		Symbol:          item.Symbol,
		Market:          item.Market,
		Theme:           theme,
		Thesis:          "已落盘消息可能构成新的研究机会，需要结合行情、估值、资金流和账户约束继续 Review。",
		EvidenceSummary: firstNonEmpty(item.Summary, item.Title),
		Confidence:      confidence,
		Status:          "candidate",
	}
}

func (s *Service) opportunityFromQuoteMove(ctx context.Context, quote storage.StockQuote) (storage.StockOpportunity, bool, error) {
	if quote.Symbol == "" || quote.LastPrice <= 0 || quote.PreviousClose <= 0 {
		return storage.StockOpportunity{}, false, nil
	}
	change := (quote.LastPrice - quote.PreviousClose) / quote.PreviousClose
	if math.Abs(change) < 0.03 {
		return storage.StockOpportunity{}, false, nil
	}
	direction := "上涨"
	if change < 0 {
		direction = "下跌"
	}
	sourceRef := fmt.Sprintf("quote:%s:%s:%s", quote.Symbol, s.marketDataDate(), direction)
	name := quote.Name
	if name == "" {
		if instrument, err := s.store.GetStockInstrument(ctx, quote.Symbol); err == nil {
			name = instrument.Name
		}
	}
	return s.createOpportunityIfMissing(ctx, storage.StockOpportunity{
		Title:           fmt.Sprintf("%s 日内%s %.2f%%", quote.Symbol, direction, math.Abs(change)*100),
		SourceType:      "quote_signal",
		SourceRefID:     sourceRef,
		Symbol:          quote.Symbol,
		Market:          quote.Market,
		Name:            name,
		Theme:           "行情异动",
		Thesis:          "实时行情出现显著波动，建议进入机会研究或建立观察策略。",
		EvidenceSummary: fmt.Sprintf("最新价 %.3f，前收 %.3f，涨跌幅 %.2f%%，成交额 %.2f", quote.LastPrice, quote.PreviousClose, change*100, quote.Amount),
		Confidence:      "medium",
		Status:          "candidate",
	})
}

func (s *Service) opportunityFromKLine(ctx context.Context, point storage.StockMarketDataPoint) (storage.StockOpportunity, bool, error) {
	if point.Symbol == "" || point.Open <= 0 || point.Close <= 0 {
		return storage.StockOpportunity{}, false, nil
	}
	change := (point.Close - point.Open) / point.Open
	if math.Abs(change) < 0.03 {
		return storage.StockOpportunity{}, false, nil
	}
	direction := "上涨"
	if change < 0 {
		direction = "下跌"
	}
	name := ""
	if instrument, err := s.store.GetStockInstrument(ctx, point.Symbol); err == nil {
		name = instrument.Name
	}
	return s.createOpportunityIfMissing(ctx, storage.StockOpportunity{
		Title:           fmt.Sprintf("%s K 线%s %.2f%%", point.Symbol, direction, math.Abs(change)*100),
		SourceType:      "market_data",
		SourceRefID:     "market_data:" + point.ID,
		Symbol:          point.Symbol,
		Market:          point.Market,
		Name:            name,
		Theme:           "K 线异动",
		Thesis:          "历史/日级 K 线出现显著波动，适合进入机会研究并等待消息面或资金面确认。",
		EvidenceSummary: fmt.Sprintf("%s 开 %.3f 收 %.3f，成交量 %.0f，质量 %s", point.DataDate, point.Open, point.Close, point.Volume, point.Quality),
		Confidence:      "medium",
		Status:          "candidate",
	})
}

func newsItemSuggestsOpportunity(item storage.StockNewsItem) bool {
	if item.Symbol == "" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(item.Importance)) {
	case "high", "urgent":
		return true
	}
	category := strings.ToLower(strings.TrimSpace(item.Category))
	switch category {
	case "policy", "earnings", "announcement", "financial_report", "research_report", "research":
		return true
	}
	haystack := strings.ToLower(strings.Join([]string{item.Title, item.Summary, item.Keywords}, " "))
	for _, keyword := range []string{"机会", "订单", "业绩", "政策", "涨价", "回购", "中标", "突破", "新高", "产能", "并购"} {
		if strings.Contains(haystack, strings.ToLower(keyword)) {
			return true
		}
	}
	return false
}

func (s *Service) governedDataSourceReady(ctx context.Context, source, sourceType string) (storage.StockDataSource, bool, string, time.Time, error) {
	src, err := s.ensureDataSource(ctx, source, sourceType)
	if err != nil {
		return storage.StockDataSource{}, false, "", time.Time{}, err
	}
	status, _, reason := sourceProbeResult(src)
	if status == "disabled" || status == "auth_required" {
		return src, false, defaultString(reason, "data source not ready"), time.Time{}, nil
	}
	if next, ok := parseStockTime(src.NextAllowedAt); ok && next.After(s.now().UTC()) {
		s.log.DebugContext(ctx, "stock: data source rate limited",
			"source", source,
			"next_allowed_at", next.Format(time.RFC3339),
		)
		return src, false, "next allowed at " + next.Format(time.RFC3339Nano), next, nil
	}
	return src, true, "", time.Time{}, nil
}

func (s *Service) recordDataSourceSuccess(ctx context.Context, src storage.StockDataSource, completedAt, status string, notes []string) (storage.StockDataSource, error) {
	base := s.now().UTC()
	if parsed, ok := parseStockTime(completedAt); ok {
		base = parsed.UTC()
	}
	return s.store.UpdateStockDataSourceHealth(ctx, storage.StockDataSource{
		Source:              src.Source,
		Status:              "available",
		Quality:             mapQuoteRefreshQuality(status),
		LastIngestedAt:      completedAt,
		NextAllowedAt:       base.Add(time.Duration(maxInt(src.RateLimitSeconds, 60)) * time.Second).Format(time.RFC3339Nano),
		ConsecutiveFailures: 0,
		FailureSummary:      limitText(strings.Join(notes, "; "), stockQuoteFailureSummaryMaxLength),
	})
}

func (s *Service) recordDataSourceFailure(ctx context.Context, src storage.StockDataSource, reason string) (storage.StockDataSource, error) {
	failures := src.ConsecutiveFailures + 1
	return s.store.UpdateStockDataSourceHealth(ctx, storage.StockDataSource{
		Source:              src.Source,
		Status:              "degraded",
		Quality:             "partial",
		NextAllowedAt:       s.now().UTC().Add(quoteProviderBackoff(src.RateLimitSeconds, failures)).Format(time.RFC3339Nano),
		ConsecutiveFailures: failures,
		FailureSummary:      limitText(reason, stockQuoteFailureSummaryMaxLength),
	})
}

func (s *Service) recordBlockedDataTask(ctx context.Context, taskType, source, symbol, requestedBy, reason string, next time.Time) (DataTaskResult, error) {
	nextRunAt := ""
	if !next.IsZero() {
		nextRunAt = next.UTC().Format(time.RFC3339Nano)
	}
	task, err := s.store.CreateStockDataTask(ctx, storage.StockDataTask{
		TaskType:       taskType,
		Source:         source,
		Symbol:         symbol,
		Status:         "blocked",
		RequestedBy:    defaultString(requestedBy, "system"),
		InputJSON:      mustJSON(map[string]any{"source": source, "symbol": symbol}),
		ResultJSON:     mustJSON(map[string]any{"processed": 0, "reason": reason}),
		FailureSummary: reason,
		NextRunAt:      nextRunAt,
	})
	if err != nil {
		return DataTaskResult{}, err
	}
	return DataTaskResult{Task: task, Notes: []string{reason}}, nil
}

func (s *Service) marketDataDate() string {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		loc = time.FixedZone("Asia/Shanghai", 8*3600)
	}
	return s.now().In(loc).Format("2006-01-02")
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
