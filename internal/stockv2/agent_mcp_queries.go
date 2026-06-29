package stockv2

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"phantom-lancer/internal/safelog"
)

func (p *agentTaskPool) mcpInstructions() string {
	if p.service == nil || p.service.store == nil {
		return "StockV2 Agent MCP Server. Use stock_agent.submit_result to submit the final result of your task."
	}
	return "StockV2 Agent MCP Server. Use stock_agent project-data tools for internal stock data. Use stock_agent.submit_result once for the final structured result."
}

func (p *agentTaskPool) mcpDataTools() []mcpTool {
	simpleSchema := func(props map[string]any) map[string]any {
		return map[string]any{
			"type":                 "object",
			"properties":           props,
			"additionalProperties": false,
		}
	}
	stringProp := func(description string) map[string]any {
		return map[string]any{"type": "string", "description": description}
	}
	limitProp := map[string]any{"type": "integer", "minimum": 1, "maximum": 100}
	return []mcpTool{
		{Name: "stock_agent.search_instruments", Description: "Search StockV2 instruments by symbol or name.", InputSchema: simpleSchema(map[string]any{"query": stringProp("Keyword, symbol, or name."), "market": stringProp("Optional market."), "instrumentType": stringProp("stock or exchange_fund."), "limit": limitProp})},
		{Name: "stock_agent.search_stock_profiles", Description: "Keyword search StockV2 stock profiles. This is not semantic vector search.", InputSchema: simpleSchema(map[string]any{"query": stringProp("Keyword."), "market": stringProp("Optional market."), "instrumentType": stringProp("stock or exchange_fund."), "limit": limitProp})},
		{Name: "stock_agent.semantic_search_stock_profiles", Description: "Semantic vector search over ready stock profile embeddings. Fails if embedding is not configured or assets are not ready.", InputSchema: simpleSchema(map[string]any{"query": stringProp("Theme or event text."), "limit": limitProp, "minScore": map[string]any{"type": "number"}})},
		{Name: "stock_agent.get_stock_profile", Description: "Get one StockV2 stock profile by symbol.", InputSchema: simpleSchema(map[string]any{"symbol": stringProp("Instrument symbol.")})},
		{Name: "stock_agent.get_latest_quotes", Description: "Get latest quotes for symbols.", InputSchema: simpleSchema(map[string]any{"symbols": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "maxItems": 50}})},
		{Name: "stock_agent.get_daily_bars_summary", Description: "Get a compact daily bar summary for one symbol.", InputSchema: simpleSchema(map[string]any{"symbol": stringProp("Instrument symbol."), "adjusted": stringProp("none, qfq, or hfq."), "limit": limitProp})},
		{Name: "stock_agent.search_news_events", Description: "Keyword search StockV2 normalized news events. This is not semantic vector search.", InputSchema: simpleSchema(map[string]any{"query": stringProp("Keyword."), "source": stringProp("Optional source."), "limit": limitProp})},
		{Name: "stock_agent.semantic_search_news_events", Description: "Semantic vector search over ready news event embeddings. Fails if embedding is not configured or assets are not ready.", InputSchema: simpleSchema(map[string]any{"query": stringProp("Theme or event text."), "limit": limitProp, "minScore": map[string]any{"type": "number"}})},
		{Name: "stock_agent.search_news_link_candidates", Description: "Search news-to-instrument link candidates.", InputSchema: simpleSchema(map[string]any{"query": stringProp("Keyword."), "symbol": stringProp("Optional symbol."), "market": stringProp("Optional market."), "limit": limitProp})},
		{Name: "stock_agent.list_existing_strategies", Description: "List existing StockV2 strategies and active strategy versions.", InputSchema: simpleSchema(map[string]any{"symbol": stringProp("Optional symbol."), "status": stringProp("Optional status."), "source": stringProp("Optional source."), "limit": limitProp})},
		{Name: "stock_agent.get_portfolio_context", Description: "Get portfolio, holding, and latest snapshot context. Does not expose credentials.", InputSchema: simpleSchema(map[string]any{"portfolioId": stringProp("Optional portfolio id.")})},
		{Name: "stock_agent.get_embedding_status", Description: "Get StockV2 embedding binding and asset status.", InputSchema: simpleSchema(map[string]any{})},
	}
}

func (p *agentTaskPool) mcpSearchInstruments(args json.RawMessage) (any, *mcpError) {
	svc, errResp := p.mcpService()
	if errResp != nil {
		return nil, errResp
	}
	var argsObj struct {
		Query          string `json:"query"`
		Market         string `json:"market"`
		InstrumentType string `json:"instrumentType"`
		Limit          int    `json:"limit"`
	}
	if err := json.Unmarshal(args, &argsObj); err != nil {
		return nil, mcpInvalidArgs(err)
	}
	limit := mcpLimit(argsObj.Limit, 20, 100)
	var items []StockV2Instrument
	var err error
	if strings.TrimSpace(argsObj.Query) == "" {
		items, err = svc.GetInstrumentsFiltered(context.Background(), argsObj.Market, argsObj.InstrumentType, "", limit, 0)
	} else {
		items, err = svc.SearchInstrumentsFiltered(context.Background(), argsObj.Query, argsObj.Market, argsObj.InstrumentType, "", limit)
	}
	if err != nil {
		return nil, mcpInternal(err)
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, map[string]any{
			"symbol": item.Symbol, "market": item.Market, "instrumentType": item.InstrumentType,
			"name": item.Name, "industry": item.Industry, "sector": item.Sector,
			"concepts": item.Concepts, "status": item.Status,
		})
	}
	return mcpJSONContent(map[string]any{"items": out, "count": len(out)})
}

func (p *agentTaskPool) mcpSearchStockProfiles(args json.RawMessage) (any, *mcpError) {
	svc, errResp := p.mcpService()
	if errResp != nil {
		return nil, errResp
	}
	var argsObj struct {
		Query          string `json:"query"`
		Market         string `json:"market"`
		InstrumentType string `json:"instrumentType"`
		Limit          int    `json:"limit"`
	}
	if err := json.Unmarshal(args, &argsObj); err != nil {
		return nil, mcpInvalidArgs(err)
	}
	items, err := svc.ListStockProfiles(context.Background(), StockProfileListFilter{
		Keyword:        argsObj.Query,
		Market:         argsObj.Market,
		InstrumentType: argsObj.InstrumentType,
		Limit:          mcpLimit(argsObj.Limit, 20, 100),
	})
	if err != nil {
		return nil, mcpInternal(err)
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, mcpStockProfileSummary(item))
	}
	return mcpJSONContent(map[string]any{"items": out, "count": len(out), "searchType": "keyword"})
}

func (p *agentTaskPool) mcpSemanticSearchStockProfiles(args json.RawMessage) (any, *mcpError) {
	svc, errResp := p.mcpService()
	if errResp != nil {
		return nil, errResp
	}
	var argsObj SemanticSearchRequest
	if err := json.Unmarshal(args, &argsObj); err != nil {
		return nil, mcpInvalidArgs(err)
	}
	items, err := svc.SemanticSearchStockProfiles(context.Background(), argsObj)
	if err != nil {
		return nil, mcpEmbeddingError(err)
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, map[string]any{
			"score":     item.Score,
			"profile":   mcpStockProfileSummary(item.Profile),
			"embedding": mcpEmbeddingAssetSummary(item.Asset),
		})
	}
	return mcpJSONContent(map[string]any{"items": out, "count": len(out), "searchType": "semantic_vector"})
}

func (p *agentTaskPool) mcpGetStockProfile(args json.RawMessage) (any, *mcpError) {
	svc, errResp := p.mcpService()
	if errResp != nil {
		return nil, errResp
	}
	var argsObj struct {
		Symbol string `json:"symbol"`
	}
	if err := json.Unmarshal(args, &argsObj); err != nil {
		return nil, mcpInvalidArgs(err)
	}
	profile, err := svc.GetStockProfile(context.Background(), argsObj.Symbol)
	if err != nil {
		return nil, mcpInternal(err)
	}
	out := mcpStockProfileSummary(profile)
	out["profileText"] = safelog.Text(profile.ProfileText, 3000)
	out["profileTextZh"] = safelog.Text(profile.ProfileTextZh, 2000)
	out["profileTextEn"] = safelog.Text(profile.ProfileTextEn, 2000)
	out["riskTagsZh"] = profile.RiskTagsZh
	out["riskTagsEn"] = profile.RiskTagsEn
	return mcpJSONContent(out)
}

func (p *agentTaskPool) mcpGetLatestQuotes(args json.RawMessage) (any, *mcpError) {
	svc, errResp := p.mcpService()
	if errResp != nil {
		return nil, errResp
	}
	var argsObj struct {
		Symbols []string `json:"symbols"`
	}
	if err := json.Unmarshal(args, &argsObj); err != nil {
		return nil, mcpInvalidArgs(err)
	}
	symbols := normalizeMCPSymbols(argsObj.Symbols, 50)
	items, err := svc.GetLatestQuotes(context.Background(), symbols)
	if err != nil {
		return nil, mcpInternal(err)
	}
	return mcpJSONContent(map[string]any{"items": items, "count": len(items)})
}

func (p *agentTaskPool) mcpGetDailyBarsSummary(args json.RawMessage) (any, *mcpError) {
	svc, errResp := p.mcpService()
	if errResp != nil {
		return nil, errResp
	}
	var argsObj struct {
		Symbol   string `json:"symbol"`
		Adjusted string `json:"adjusted"`
		Limit    int    `json:"limit"`
	}
	if err := json.Unmarshal(args, &argsObj); err != nil {
		return nil, mcpInvalidArgs(err)
	}
	adjusted := strings.TrimSpace(argsObj.Adjusted)
	if adjusted == "" {
		adjusted = DailyBarAdjustedNone
	}
	bars, err := svc.GetDailyBars(context.Background(), argsObj.Symbol, mcpLimit(argsObj.Limit, 60, 250), "", "", adjusted)
	if err != nil {
		return nil, mcpInternal(err)
	}
	return mcpJSONContent(dailyBarsSummary(argsObj.Symbol, adjusted, bars))
}

func (p *agentTaskPool) mcpSearchNewsEvents(args json.RawMessage) (any, *mcpError) {
	svc, errResp := p.mcpService()
	if errResp != nil {
		return nil, errResp
	}
	var argsObj struct {
		Query  string `json:"query"`
		Source string `json:"source"`
		Limit  int    `json:"limit"`
	}
	if err := json.Unmarshal(args, &argsObj); err != nil {
		return nil, mcpInvalidArgs(err)
	}
	items, err := svc.ListNewsEvents(context.Background(), NewsEventListFilter{Query: argsObj.Query, Source: argsObj.Source, Limit: mcpLimit(argsObj.Limit, 20, 100)})
	if err != nil {
		return nil, mcpInternal(err)
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, mcpNewsEventSummary(item))
	}
	return mcpJSONContent(map[string]any{"items": out, "count": len(out), "searchType": "keyword"})
}

func (p *agentTaskPool) mcpSemanticSearchNewsEvents(args json.RawMessage) (any, *mcpError) {
	svc, errResp := p.mcpService()
	if errResp != nil {
		return nil, errResp
	}
	var argsObj SemanticSearchRequest
	if err := json.Unmarshal(args, &argsObj); err != nil {
		return nil, mcpInvalidArgs(err)
	}
	items, err := svc.SemanticSearchNewsEvents(context.Background(), argsObj)
	if err != nil {
		return nil, mcpEmbeddingError(err)
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, map[string]any{
			"score":     item.Score,
			"event":     mcpNewsEventSummary(item.Event),
			"embedding": mcpEmbeddingAssetSummary(item.Asset),
		})
	}
	return mcpJSONContent(map[string]any{"items": out, "count": len(out), "searchType": "semantic_vector"})
}

func (p *agentTaskPool) mcpSearchNewsLinkCandidates(args json.RawMessage) (any, *mcpError) {
	svc, errResp := p.mcpService()
	if errResp != nil {
		return nil, errResp
	}
	var argsObj struct {
		Query  string `json:"query"`
		Symbol string `json:"symbol"`
		Market string `json:"market"`
		Limit  int    `json:"limit"`
	}
	if err := json.Unmarshal(args, &argsObj); err != nil {
		return nil, mcpInvalidArgs(err)
	}
	items, err := svc.ListNewsLinkCandidates(context.Background(), NewsLinkCandidateListFilter{
		Query: argsObj.Query, Symbol: argsObj.Symbol, Market: argsObj.Market, Limit: mcpLimit(argsObj.Limit, 20, 100),
	})
	if err != nil {
		return nil, mcpInternal(err)
	}
	return mcpJSONContent(map[string]any{"items": items, "count": len(items)})
}

func (p *agentTaskPool) mcpListExistingStrategies(args json.RawMessage) (any, *mcpError) {
	svc, errResp := p.mcpService()
	if errResp != nil {
		return nil, errResp
	}
	var argsObj struct {
		Symbol string `json:"symbol"`
		Status string `json:"status"`
		Source string `json:"source"`
		Limit  int    `json:"limit"`
	}
	if err := json.Unmarshal(args, &argsObj); err != nil {
		return nil, mcpInvalidArgs(err)
	}
	items, err := svc.ListStrategies(context.Background(), StrategyListFilter{
		Symbol: argsObj.Symbol, Status: argsObj.Status, Source: argsObj.Source, Limit: mcpLimit(argsObj.Limit, 20, 100),
	})
	if err != nil {
		return nil, mcpInternal(err)
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		entry := map[string]any{"strategy": item.Strategy}
		if item.ActiveVersion != nil {
			entry["activeVersion"] = map[string]any{
				"id": item.ActiveVersion.ID, "versionNo": item.ActiveVersion.VersionNo,
				"title": item.ActiveVersion.Title, "direction": item.ActiveVersion.Direction,
				"thesis":         safelog.Text(item.ActiveVersion.Thesis, 1200),
				"riskNotes":      safelog.Text(item.ActiveVersion.RiskNotes, 800),
				"generationMeta": item.ActiveVersion.GenerationMeta,
			}
		}
		out = append(out, entry)
	}
	return mcpJSONContent(map[string]any{"items": out, "count": len(out)})
}

func (p *agentTaskPool) mcpGetPortfolioContext(args json.RawMessage) (any, *mcpError) {
	svc, errResp := p.mcpService()
	if errResp != nil {
		return nil, errResp
	}
	var argsObj struct {
		PortfolioID string `json:"portfolioId"`
	}
	if err := json.Unmarshal(args, &argsObj); err != nil {
		return nil, mcpInvalidArgs(err)
	}
	var portfolios []StockV2Portfolio
	var err error
	if strings.TrimSpace(argsObj.PortfolioID) != "" {
		item, getErr := svc.store.GetPortfolio(context.Background(), argsObj.PortfolioID)
		if getErr != nil {
			return nil, mcpInternal(getErr)
		}
		portfolios = []StockV2Portfolio{item}
	} else {
		portfolios, err = svc.store.ListPortfolios(context.Background())
		if err != nil {
			return nil, mcpInternal(err)
		}
	}
	out := make([]map[string]any, 0, len(portfolios))
	for _, portfolio := range portfolios {
		holdings, _ := svc.store.ListHoldings(context.Background(), portfolio.ID)
		snapshots, _ := svc.store.GetPortfolioSnapshots(context.Background(), portfolio.ID, 1)
		entry := map[string]any{"portfolio": portfolio, "holdings": holdings}
		if len(snapshots) > 0 {
			entry["latestSnapshot"] = snapshots[0]
		}
		out = append(out, entry)
	}
	return mcpJSONContent(map[string]any{"items": out, "count": len(out)})
}

func (p *agentTaskPool) mcpGetEmbeddingStatus(args json.RawMessage) (any, *mcpError) {
	svc, errResp := p.mcpService()
	if errResp != nil {
		return nil, errResp
	}
	status, err := svc.GetEmbeddingStatus(context.Background())
	if err != nil {
		return nil, mcpInternal(err)
	}
	return mcpJSONContent(status)
}

func (p *agentTaskPool) mcpService() (*Service, *mcpError) {
	if p == nil || p.service == nil || p.service.store == nil {
		return nil, &mcpError{Code: mcpErrInternal, Message: "stockv2 service is not configured"}
	}
	return p.service, nil
}

func mcpJSONContent(value any) (any, *mcpError) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, mcpInternal(err)
	}
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": string(raw)}},
		"isError": false,
	}, nil
}

func mcpInvalidArgs(err error) *mcpError {
	return &mcpError{Code: mcpErrInvalidParams, Message: "Invalid arguments: " + err.Error()}
}

func mcpInternal(err error) *mcpError {
	return &mcpError{Code: mcpErrInternal, Message: safelog.Text(err.Error(), 500)}
}

func mcpEmbeddingError(err error) *mcpError {
	if code := embeddingErrorCode(err); code != "" {
		return &mcpError{Code: mcpErrInvalidParams, Message: code, Data: map[string]string{"code": code}}
	}
	if errors.Is(err, ErrInvalidEmbeddingRequest) {
		return &mcpError{Code: mcpErrInvalidParams, Message: err.Error()}
	}
	return mcpInternal(err)
}

func mcpLimit(value, def, max int) int {
	if value <= 0 {
		value = def
	}
	if value > max {
		value = max
	}
	return value
}

func normalizeMCPSymbols(values []string, max int) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
		if len(out) >= max {
			break
		}
	}
	return out
}

func mcpStockProfileSummary(profile StockProfile) map[string]any {
	return map[string]any{
		"symbol": profile.Symbol, "market": profile.Market, "instrumentType": profile.InstrumentType,
		"name": profile.Name, "industry": profile.Industry, "sectors": profile.Sectors,
		"concepts": profile.Concepts, "tags": profile.Tags,
		"businessSummary":   safelog.Text(profile.BusinessSummary, 1200),
		"businessSummaryZh": safelog.Text(profile.BusinessSummaryZh, 1200),
		"businessSummaryEn": safelog.Text(profile.BusinessSummaryEn, 1200),
		"keywordsZh":        profile.KeywordsZh, "keywordsEn": profile.KeywordsEn,
		"theme": profile.Theme, "fundType": profile.FundType, "trackingIndex": profile.TrackingIndex,
		"updatedAt": profile.UpdatedAt,
	}
}

func mcpNewsEventSummary(event NewsEvent) map[string]any {
	return map[string]any{
		"id": event.ID, "source": event.Source, "externalId": event.ExternalID,
		"title": safelog.Text(event.Title, 500), "summary": safelog.Text(event.Summary, 1000),
		"contentSnippet": safelog.Text(event.Content, 1200),
		"url":            safelog.URLLabel(event.URL), "qualityStatus": event.QualityStatus,
		"linkStatus": event.LinkStatus, "eventAt": event.EventAt,
	}
}

func mcpEmbeddingAssetSummary(asset EmbeddingAsset) map[string]any {
	return map[string]any{
		"assetId": asset.ID, "objectType": asset.ObjectType, "objectId": asset.ObjectID,
		"modelId": asset.ModelID, "providerId": asset.ProviderID,
		"protocol": asset.EmbeddingProtocol, "dimensions": asset.EmbeddingDimensions,
		"textHash": asset.TextHash, "status": asset.Status, "vectorRef": asset.VectorRef,
	}
}

func dailyBarsSummary(symbol, adjusted string, bars []StockV2DailyBar) map[string]any {
	summary := map[string]any{"symbol": strings.TrimSpace(symbol), "adjusted": adjusted, "rowCount": len(bars)}
	if len(bars) == 0 {
		return summary
	}
	first := bars[0]
	latest := bars[len(bars)-1]
	minClose := first.Close
	maxClose := first.Close
	var totalVolume float64
	for _, bar := range bars {
		if bar.Close < minClose {
			minClose = bar.Close
		}
		if bar.Close > maxClose {
			maxClose = bar.Close
		}
		totalVolume += bar.Volume
	}
	summary["earliestDate"] = first.TradeDate
	summary["latestDate"] = latest.TradeDate
	summary["latestClose"] = latest.Close
	summary["latestPctChange"] = latest.PctChange
	summary["minClose"] = minClose
	summary["maxClose"] = maxClose
	summary["totalVolume"] = totalVolume
	summary["latestQuality"] = latest.Quality
	summary["source"] = latest.Source
	return summary
}
