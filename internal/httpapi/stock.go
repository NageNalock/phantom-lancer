package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"

	stocksvc "phantom-lancer/internal/stock"
	"phantom-lancer/internal/storage"
)

const maxStockBodyBytes = 256 << 10

func (s *Server) stockService() *stocksvc.Service {
	if s.stock == nil {
		s.stock = stocksvc.NewService(s.store, s.log)
	}
	return s.stock
}

func (s *Server) handleStockSnapshot(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuth(w, r); !ok {
		return
	}
	snapshot, err := s.stockService().Snapshot(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "stock_snapshot_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func (s *Server) handleStockPortfolios(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		items, err := s.store.ListStockPortfolios(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "stock_portfolios_read_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	case http.MethodPost:
		if !s.requireCSRF(w, r, ctx.Session) {
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxStockBodyBytes)
		var req storage.StockPortfolio
		if !decodeJSON(w, r, &req) {
			return
		}
		item, err := s.store.CreateStockPortfolio(r.Context(), req)
		if err != nil {
			writeError(w, http.StatusBadRequest, "stock_portfolio_create_failed", err.Error())
			return
		}
		_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
			EventType: "stock.portfolio.created",
			RiskLevel: "medium",
			Summary:   "已创建股票账户/仓位组合",
			Payload:   map[string]any{"portfolioId": item.ID, "name": item.Name, "cash": item.Cash},
		})
		writeJSON(w, http.StatusCreated, map[string]any{"portfolio": item})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "方法不允许")
	}
}

func (s *Server) handleStockPortfolioSubroutes(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/stock/portfolios/"), "/")
	parts := strings.Split(path, "/")
	if len(parts) == 1 && parts[0] != "" && r.Method == http.MethodPatch {
		if !s.requireCSRF(w, r, ctx.Session) {
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxStockBodyBytes)
		var req storage.StockPortfolioPatch
		if !decodeJSON(w, r, &req) {
			return
		}
		result, err := s.store.UpdateStockPortfolio(r.Context(), parts[0], req)
		if err != nil {
			switch {
			case errors.Is(err, storage.ErrNotFound):
				writeError(w, http.StatusNotFound, "stock_portfolio_not_found", "股票账户不存在")
			default:
				writeError(w, http.StatusBadRequest, "stock_portfolio_update_failed", err.Error())
			}
			return
		}
		cashDelta := result.Portfolio.Cash - result.Before.Cash
		_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
			EventType: "stock.portfolio.updated",
			RiskLevel: "medium",
			Summary:   "已更新股票账户/仓位组合",
			Payload: map[string]any{
				"portfolioId":              result.Portfolio.ID,
				"name":                     result.Portfolio.Name,
				"cashBefore":               result.Before.Cash,
				"cashAfter":                result.Portfolio.Cash,
				"cashDelta":                cashDelta,
				"riskLevel":                result.Portfolio.RiskLevel,
				"maxSinglePositionPct":     result.Portfolio.MaxSinglePositionPct,
				"maxDrawdownPct":           result.Portfolio.MaxDrawdownPct,
				"operationPermissionsFrom": mapStockPortfolioPermissions(result.Before),
				"operationPermissionsTo":   mapStockPortfolioPermissions(result.Portfolio),
			},
		})
		writeJSON(w, http.StatusOK, map[string]any{"portfolio": result.Portfolio})
		return
	}
	if len(parts) == 1 && parts[0] != "" && r.Method == http.MethodDelete {
		if !s.requireCSRF(w, r, ctx.Session) {
			return
		}
		deleted, err := s.store.DeleteStockPortfolio(r.Context(), parts[0])
		if err != nil {
			switch {
			case errors.Is(err, storage.ErrNotFound):
				writeError(w, http.StatusNotFound, "stock_portfolio_not_found", "股票账户不存在")
			case errors.Is(err, storage.ErrStockPortfolioInUse):
				detail := strings.TrimPrefix(err.Error(), storage.ErrStockPortfolioInUse.Error()+": ")
				if detail != "" {
					detail = "：" + detail
				}
				writeError(w, http.StatusConflict, "stock_portfolio_in_use", "账户仍被策略、盯盘或历史记录引用，不能删除"+detail)
			default:
				writeError(w, http.StatusInternalServerError, "stock_portfolio_delete_failed", err.Error())
			}
			return
		}
		_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
			EventType: "stock.portfolio.deleted",
			RiskLevel: "high",
			Summary:   "已删除股票账户/仓位组合",
			Payload:   map[string]any{"portfolioId": deleted.Portfolio.ID, "name": deleted.Portfolio.Name, "holdingsDeleted": deleted.HoldingsDeleted},
		})
		writeJSON(w, http.StatusOK, map[string]any{"deleted": deleted})
		return
	}
	if len(parts) != 2 || parts[1] != "holdings" || r.Method != http.MethodPost {
		writeError(w, http.StatusNotFound, "stock_portfolio_route_not_found", "未找到股票账户路由")
		return
	}
	if !s.requireCSRF(w, r, ctx.Session) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxStockBodyBytes)
	var req storage.StockHolding
	if !decodeJSON(w, r, &req) {
		return
	}
	req.PortfolioID = parts[0]
	item, err := s.store.UpsertStockHolding(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusBadRequest, "stock_holding_save_failed", err.Error())
		return
	}
	_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
		EventType: "stock.holding.saved",
		RiskLevel: "medium",
		Summary:   "已保存股票持仓",
		Payload:   map[string]any{"portfolioId": item.PortfolioID, "symbol": item.Symbol, "quantity": item.Quantity},
	})
	writeJSON(w, http.StatusOK, map[string]any{"holding": item})
}

func mapStockPortfolioPermissions(portfolio storage.StockPortfolio) map[string]bool {
	return map[string]bool{
		"allowBuy":    portfolio.AllowBuy,
		"allowAdd":    portfolio.AllowAdd,
		"allowReduce": portfolio.AllowReduce,
		"allowSell":   portfolio.AllowSell,
	}
}

func (s *Server) handleStockQuotes(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		items, err := s.store.ListStockQuotes(r.Context(), parseInt(r.URL.Query().Get("limit")))
		if err != nil {
			writeError(w, http.StatusInternalServerError, "stock_quotes_read_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	case http.MethodPost:
		if !s.requireCSRF(w, r, ctx.Session) {
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxStockBodyBytes)
		var req storage.StockQuote
		if !decodeJSON(w, r, &req) {
			return
		}
		item, err := s.store.UpsertStockQuote(r.Context(), req)
		if err != nil {
			writeError(w, http.StatusBadRequest, "stock_quote_save_failed", err.Error())
			return
		}
		_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
			EventType: "stock.quote.saved",
			RiskLevel: "low",
			Summary:   "已保存股票行情快照",
			Payload:   map[string]any{"symbol": item.Symbol, "lastPrice": item.LastPrice, "freshness": item.DataFreshness},
		})
		writeJSON(w, http.StatusOK, map[string]any{"quote": item})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "方法不允许")
	}
}

func (s *Server) handleStockQuoteRefresh(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, ctx.Session) {
		return
	}
	result, err := s.stockService().RecordQuoteRefreshStatus(r.Context(), "manual")
	if err != nil {
		writeError(w, http.StatusBadRequest, "stock_quote_refresh_failed", err.Error())
		return
	}
	_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
		EventType: "stock.quote_refresh.checked",
		RiskLevel: "low",
		Summary:   "已检查股票实时行情刷新状态",
		Payload:   map[string]any{"taskId": result.Task.ID, "status": result.Task.Status, "source": result.Task.Source},
	})
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleStockDataSources(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		items, err := s.store.ListStockDataSources(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "stock_sources_read_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	case http.MethodPost:
		if !s.requireCSRF(w, r, ctx.Session) {
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxStockBodyBytes)
		var req storage.StockDataSource
		if !decodeJSON(w, r, &req) {
			return
		}
		if req.AuthMode != "disabled" && !req.Enabled {
			req.Enabled = true
		}
		item, err := s.stockService().UpsertDataSource(r.Context(), req)
		if err != nil {
			writeError(w, http.StatusBadRequest, "stock_source_save_failed", err.Error())
			return
		}
		_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
			EventType: "stock.data_source.saved",
			RiskLevel: "low",
			Summary:   "已保存股票数据源",
			Payload:   map[string]any{"source": item.Source, "sourceType": item.SourceType, "authMode": item.AuthMode, "status": item.Status},
		})
		writeJSON(w, http.StatusOK, map[string]any{"source": item})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "方法不允许")
	}
}

func (s *Server) handleStockDataSourceSubroutes(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/stock/data-sources/"), "/")
	parts := strings.Split(path, "/")
	if len(parts) != 2 || parts[1] != "health-check" || r.Method != http.MethodPost {
		writeError(w, http.StatusNotFound, "stock_source_route_not_found", "未找到数据源路由")
		return
	}
	if !s.requireCSRF(w, r, ctx.Session) {
		return
	}
	result, err := s.stockService().RunDataSourceHealthCheck(r.Context(), parts[0])
	if err != nil {
		writeError(w, http.StatusBadRequest, "stock_source_check_failed", err.Error())
		return
	}
	_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
		EventType: "stock.data_source.checked",
		RiskLevel: "low",
		Summary:   "已检查股票数据源健康状态",
		Payload:   map[string]any{"source": parts[0], "status": result.Task.Status, "processed": result.Task.ProcessedCount},
	})
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleStockInstrumentSearch(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuth(w, r); !ok {
		return
	}
	q := r.URL.Query()
	pageSize := parseInt(q.Get("pageSize"))
	if pageSize == 0 {
		pageSize = parseInt(q.Get("page_size"))
	}
	result, err := s.store.SearchStockInstruments(r.Context(), storage.StockInstrumentSearchParams{
		Query:           q.Get("q"),
		Markets:         q["market"],
		Statuses:        q["status"],
		Industry:        q.Get("industry"),
		Concepts:        q["concept"],
		MinListingDate:  firstNonEmptyQuery(q, "minListingDate", "min_listing_date"),
		Quality:         q.Get("quality"),
		IncludeDelisted: q.Get("include_delisted") == "true" || q.Get("includeDelisted") == "true",
		Sort:            q.Get("sort"),
		Page:            parseInt(q.Get("page")),
		PageSize:        pageSize,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "stock_instrument_search_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func firstNonEmptyQuery(q map[string][]string, keys ...string) string {
	for _, key := range keys {
		for _, value := range q[key] {
			if strings.TrimSpace(value) != "" {
				return value
			}
		}
	}
	return ""
}

func (s *Server) handleStockInstrumentIndustries(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuth(w, r); !ok {
		return
	}
	items, err := s.store.DistinctStockIndustries(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "stock_industries_read_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleStockInstruments(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		includeDelisted := true
		if raw := firstNonEmptyQuery(r.URL.Query(), "include_delisted", "includeDelisted"); raw != "" {
			includeDelisted = strings.EqualFold(strings.TrimSpace(raw), "true")
		}
		items, err := s.store.ListStockInstrumentsFiltered(r.Context(), parseInt(r.URL.Query().Get("limit")), includeDelisted)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "stock_instruments_read_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	case http.MethodPost:
		if !s.requireCSRF(w, r, ctx.Session) {
			return
		}
		var req struct {
			Source string                    `json:"source"`
			Items  []storage.StockInstrument `json:"items"`
			Auto   bool                      `json:"auto"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxStockBodyBytes)
		if !decodeJSON(w, r, &req) {
			return
		}
		var result stocksvc.DataTaskResult
		var err error
		if req.Auto && req.Source == "eastmoney_universe" {
			result, err = s.stockService().RefreshAStockUniverse(r.Context(), stocksvc.MaintenanceModeManual, "manual")
		} else {
			result, err = s.stockService().RefreshInstruments(r.Context(), req.Source, req.Items)
		}
		if err != nil {
			writeError(w, http.StatusBadRequest, "stock_instruments_refresh_failed", err.Error())
			return
		}
		_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
			EventType: "stock.instrument.refreshed",
			RiskLevel: "low",
			Summary:   "已刷新股票主数据",
			Payload:   map[string]any{"source": req.Source, "processed": result.Task.ProcessedCount, "failed": result.Task.FailedCount},
		})
		writeJSON(w, http.StatusOK, result)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "方法不允许")
	}
}

func (s *Server) handleStockMarketData(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		items, err := s.store.ListStockMarketDataPoints(r.Context(), r.URL.Query().Get("symbol"), r.URL.Query().Get("dataset"), parseInt(r.URL.Query().Get("limit")))
		if err != nil {
			writeError(w, http.StatusInternalServerError, "stock_market_data_read_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	case http.MethodPost:
		if !s.requireCSRF(w, r, ctx.Session) {
			return
		}
		var req struct {
			Source string                         `json:"source"`
			Points []storage.StockMarketDataPoint `json:"points"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxStockBodyBytes)
		if !decodeJSON(w, r, &req) {
			return
		}
		result, err := s.stockService().BackfillMarketData(r.Context(), req.Source, req.Points)
		if err != nil {
			writeError(w, http.StatusBadRequest, "stock_market_data_backfill_failed", err.Error())
			return
		}
		_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
			EventType: "stock.market_data.backfilled",
			RiskLevel: "low",
			Summary:   "已写入股票历史/指标数据",
			Payload:   map[string]any{"source": req.Source, "processed": result.Task.ProcessedCount, "failed": result.Task.FailedCount},
		})
		writeJSON(w, http.StatusOK, result)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "方法不允许")
	}
}

func (s *Server) handleStockNews(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuth(w, r); !ok {
		return
	}
	items, err := s.store.ListStockNewsItems(r.Context(), r.URL.Query().Get("source"), r.URL.Query().Get("symbol"), parseInt(r.URL.Query().Get("limit")))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "stock_news_read_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleStockNewsIngest(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, ctx.Session) {
		return
	}
	var req struct {
		Source string                  `json:"source"`
		Items  []storage.StockNewsItem `json:"items"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxStockBodyBytes)
	if !decodeJSON(w, r, &req) {
		return
	}
	result, err := s.stockService().IngestNews(r.Context(), req.Source, req.Items)
	if err != nil {
		writeError(w, http.StatusBadRequest, "stock_news_ingest_failed", err.Error())
		return
	}
	_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
		EventType: "stock.news.ingested",
		RiskLevel: "medium",
		Summary:   "已采集股票消息面数据",
		Payload:   map[string]any{"source": req.Source, "processed": result.Task.ProcessedCount, "alerts": len(result.Alerts), "failed": result.Task.FailedCount},
	})
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleStockDataTasks(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuth(w, r); !ok {
		return
	}
	items, err := s.store.ListStockDataTasks(r.Context(), parseInt(r.URL.Query().Get("limit")))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "stock_data_tasks_read_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleStockDataTasksRun(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, ctx.Session) {
		return
	}
	result, err := s.stockService().RunDataMaintenance(r.Context(), "manual")
	if err != nil {
		writeError(w, http.StatusBadRequest, "stock_data_tasks_run_failed", err.Error())
		return
	}
	_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
		EventType: "stock.data_tasks.run",
		RiskLevel: "medium",
		Summary:   "已执行股票数据维护任务",
		Payload:   map[string]any{"tasks": len(result.Tasks), "opportunities": len(result.Opportunities), "marketData": len(result.MarketData), "alerts": len(result.Alerts)},
	})
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleStockOpportunities(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		items, err := s.store.ListStockOpportunities(r.Context(), r.URL.Query().Get("status"), parseInt(r.URL.Query().Get("limit")))
		if err != nil {
			writeError(w, http.StatusInternalServerError, "stock_opportunities_read_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	case http.MethodPost:
		if !s.requireCSRF(w, r, ctx.Session) {
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxStockBodyBytes)
		var req storage.StockOpportunity
		if !decodeJSON(w, r, &req) {
			return
		}
		item, err := s.store.CreateStockOpportunity(r.Context(), req)
		if err != nil {
			writeError(w, http.StatusBadRequest, "stock_opportunity_create_failed", err.Error())
			return
		}
		_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
			EventType: "stock.opportunity.created",
			RiskLevel: "medium",
			Summary:   "已创建股票候选机会",
			Payload:   map[string]any{"opportunityId": item.ID, "symbol": item.Symbol, "sourceType": item.SourceType},
		})
		writeJSON(w, http.StatusCreated, map[string]any{"opportunity": item})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "方法不允许")
	}
}

func (s *Server) handleStockOpportunitiesDiscover(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, ctx.Session) {
		return
	}
	result, err := s.stockService().DiscoverOpportunities(r.Context(), "manual")
	if err != nil {
		writeError(w, http.StatusBadRequest, "stock_opportunity_discovery_failed", err.Error())
		return
	}
	_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
		EventType: "stock.opportunity.discovery_run",
		RiskLevel: "medium",
		Summary:   "已执行股票自动机会发现",
		Payload:   map[string]any{"taskId": result.Task.ID, "created": len(result.Opportunities), "processed": result.Task.ProcessedCount},
	})
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleStockOpportunitySubroutes(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/stock/opportunities/"), "/")
	parts := strings.Split(path, "/")
	if len(parts) != 2 || parts[1] != "strategy" || r.Method != http.MethodPost {
		writeError(w, http.StatusNotFound, "stock_opportunity_route_not_found", "未找到机会路由")
		return
	}
	if !s.requireCSRF(w, r, ctx.Session) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxStockBodyBytes)
	var req storage.StockStrategy
	if !decodeJSON(w, r, &req) {
		return
	}
	opportunity, strategy, err := s.stockService().CreateStrategyFromOpportunity(r.Context(), parts[0], req)
	if err != nil {
		writeError(w, http.StatusBadRequest, "stock_opportunity_strategy_failed", err.Error())
		return
	}
	_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
		EventType: "stock.opportunity.strategy_created",
		RiskLevel: "medium",
		Summary:   "已从股票机会生成策略",
		Payload:   map[string]any{"opportunityId": opportunity.ID, "strategyId": strategy.ID, "symbol": strategy.Symbol},
	})
	writeJSON(w, http.StatusCreated, map[string]any{"opportunity": opportunity, "strategy": strategy})
}

func (s *Server) handleStockStrategies(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		items, err := s.store.ListStockStrategies(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "stock_strategies_read_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	case http.MethodPost:
		if !s.requireCSRF(w, r, ctx.Session) {
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxStockBodyBytes)
		var req storage.StockStrategy
		if !decodeJSON(w, r, &req) {
			return
		}
		item, err := s.store.CreateStockStrategy(r.Context(), req)
		if err != nil {
			writeError(w, http.StatusBadRequest, "stock_strategy_create_failed", err.Error())
			return
		}
		_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
			EventType: "stock.strategy.created",
			RiskLevel: "medium",
			Summary:   "已创建股票策略",
			Payload:   map[string]any{"strategyId": item.ID, "symbol": item.Symbol, "strategyType": item.StrategyType},
		})
		writeJSON(w, http.StatusCreated, map[string]any{"strategy": item})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "方法不允许")
	}
}

func (s *Server) handleStockStrategySubroutes(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/stock/strategies/"), "/")
	parts := strings.Split(path, "/")
	if len(parts) != 2 || parts[1] != "watch" || r.Method != http.MethodPost {
		writeError(w, http.StatusNotFound, "stock_strategy_route_not_found", "未找到策略路由")
		return
	}
	if !s.requireCSRF(w, r, ctx.Session) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxStockBodyBytes)
	var req storage.StockWatch
	if !decodeJSON(w, r, &req) {
		return
	}
	req.StrategyID = parts[0]
	item, err := s.store.CreateStockWatch(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusBadRequest, "stock_watch_create_failed", err.Error())
		return
	}
	_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
		EventType: "stock.watch.created",
		RiskLevel: "medium",
		Summary:   "已从策略创建股票盯盘",
		Payload:   map[string]any{"watchId": item.ID, "strategyId": item.StrategyID, "symbol": item.Symbol},
	})
	writeJSON(w, http.StatusCreated, map[string]any{"watch": item})
}

func (s *Server) handleStockWatches(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuth(w, r); !ok {
		return
	}
	items, err := s.store.ListStockWatches(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "stock_watches_read_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleStockWatchSubroutes(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPatch {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "方法不允许")
		return
	}
	if !s.requireCSRF(w, r, ctx.Session) {
		return
	}
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/stock/watches/"), "/")
	if id == "" || strings.Contains(id, "/") {
		writeError(w, http.StatusNotFound, "stock_watch_route_not_found", "未找到盯盘路由")
		return
	}
	var req struct {
		Status               *string  `json:"status"`
		CheckIntervalSeconds *int     `json:"checkIntervalSeconds"`
		TriggerPriceAbove    *float64 `json:"triggerPriceAbove"`
		TriggerPriceBelow    *float64 `json:"triggerPriceBelow"`
		CooldownSeconds      *int     `json:"cooldownSeconds"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxStockBodyBytes)
	if !decodeJSON(w, r, &req) {
		return
	}
	item, err := s.store.UpdateStockWatchFields(r.Context(), id, storage.StockWatchUpdate{
		Status:               req.Status,
		CheckIntervalSeconds: req.CheckIntervalSeconds,
		TriggerPriceAbove:    req.TriggerPriceAbove,
		TriggerPriceBelow:    req.TriggerPriceBelow,
		CooldownSeconds:      req.CooldownSeconds,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, "stock_watch_update_failed", err.Error())
		return
	}
	_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
		EventType: "stock.watch.updated",
		RiskLevel: "medium",
		Summary:   "已更新股票盯盘任务",
		Payload:   map[string]any{"watchId": item.ID, "status": item.Status, "interval": item.CheckIntervalSeconds, "cooldown": item.CooldownSeconds},
	})
	writeJSON(w, http.StatusOK, map[string]any{"watch": item})
}

func (s *Server) handleStockWatchCheck(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, ctx.Session) {
		return
	}
	var req struct {
		Force bool `json:"force"`
	}
	if r.Body != nil {
		r.Body = http.MaxBytesReader(w, r.Body, maxStockBodyBytes)
		if ok, err := decodeOptionalJSON(w, r, &req); err != nil {
			return
		} else if !ok {
			return
		}
	}
	result, err := s.stockService().CheckWatches(r.Context(), req.Force)
	if err != nil {
		writeError(w, http.StatusBadRequest, "stock_watch_check_failed", err.Error())
		return
	}
	_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
		EventType: "stock.watch.checked",
		RiskLevel: "low",
		Summary:   "已执行股票盯盘检查",
		Payload:   map[string]any{"checked": result.Checked, "alerts": len(result.Alerts), "force": req.Force, "session": result.MarketClock.Session},
	})
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleStockAlerts(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuth(w, r); !ok {
		return
	}
	items, err := s.store.ListStockAlerts(r.Context(), r.URL.Query().Get("status"), parseInt(r.URL.Query().Get("limit")))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "stock_alerts_read_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleStockAlertSubroutes(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/stock/alerts/"), "/")
	parts := strings.Split(path, "/")
	if len(parts) == 1 && r.Method == http.MethodPatch {
		if !s.requireCSRF(w, r, ctx.Session) {
			return
		}
		var req struct {
			Status        string `json:"status"`
			SnoozeSeconds int    `json:"snoozeSeconds"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxStockBodyBytes)
		if !decodeJSON(w, r, &req) {
			return
		}
		item, err := s.store.UpdateStockAlertLifecycle(r.Context(), parts[0], req.Status, req.SnoozeSeconds)
		if err != nil {
			writeError(w, http.StatusBadRequest, "stock_alert_update_failed", err.Error())
			return
		}
		_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
			EventType: "stock.alert.updated",
			RiskLevel: "medium",
			Summary:   "已更新股票提醒状态",
			Payload:   map[string]any{"alertId": item.ID, "status": item.Status, "snoozeSeconds": req.SnoozeSeconds},
		})
		writeJSON(w, http.StatusOK, map[string]any{"alert": item})
		return
	}
	if len(parts) == 2 && parts[1] == "review" && r.Method == http.MethodPost {
		if !s.requireCSRF(w, r, ctx.Session) {
			return
		}
		result, err := s.stockService().ReviewAlert(r.Context(), parts[0])
		if err != nil {
			writeError(w, http.StatusBadRequest, "stock_review_failed", err.Error())
			return
		}
		agentRunID := ""
		strategyPatchID := ""
		if result.AgentRun != nil {
			agentRunID = result.AgentRun.ID
		}
		if result.StrategyPatch != nil {
			strategyPatchID = result.StrategyPatch.ID
		}
		_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
			EventType: "stock.review.created",
			RiskLevel: "high",
			Summary:   "已生成股票操作 Review",
			Payload:   map[string]any{"reviewId": result.Review.ID, "alertId": result.Review.AlertID, "result": result.Review.ReviewResult, "guardrail": result.Review.GuardrailResult, "agentRunId": agentRunID, "strategyPatchId": strategyPatchID},
		})
		writeJSON(w, http.StatusCreated, result)
		return
	}
	writeError(w, http.StatusNotFound, "stock_alert_route_not_found", "未找到提醒路由")
}

func (s *Server) handleStockReviews(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuth(w, r); !ok {
		return
	}
	items, err := s.store.ListStockReviews(r.Context(), parseInt(r.URL.Query().Get("limit")))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "stock_reviews_read_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleStockAgentModelProfiles(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		items, err := s.store.ListStockAgentModelProfiles(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "stock_agent_profiles_read_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	case http.MethodPost:
		if !s.requireCSRF(w, r, ctx.Session) {
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxStockBodyBytes)
		var req storage.StockAgentModelProfile
		if !decodeJSON(w, r, &req) {
			return
		}
		item, err := s.stockService().UpsertAgentModelProfile(r.Context(), req)
		if err != nil {
			writeError(w, http.StatusBadRequest, "stock_agent_profile_save_failed", err.Error())
			return
		}
		_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
			EventType: "stock.agent_profile.saved",
			RiskLevel: "medium",
			Summary:   "已保存股票 Agent 模型配置",
			Payload:   map[string]any{"profileId": item.ID, "provider": item.Provider, "model": item.Model, "taskType": item.TaskType, "protocol": item.DecisionProtocol, "enabled": item.Enabled},
		})
		writeJSON(w, http.StatusOK, map[string]any{"profile": item})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "方法不允许")
	}
}

func (s *Server) handleStockAgentRuns(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuth(w, r); !ok {
		return
	}
	items, err := s.store.ListStockAgentRuns(r.Context(), parseInt(r.URL.Query().Get("limit")))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "stock_agent_runs_read_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleStockAgentAuthorizations(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuth(w, r); !ok {
		return
	}
	items, err := s.store.ListStockAgentAuthorizations(r.Context(), r.URL.Query().Get("status"), parseInt(r.URL.Query().Get("limit")))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "stock_agent_authorizations_read_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleStockAgentAuthorizationSubroutes(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/stock/agent/authorizations/"), "/")
	parts := strings.Split(path, "/")
	if len(parts) != 2 || r.Method != http.MethodPost {
		writeError(w, http.StatusNotFound, "stock_agent_authorization_route_not_found", "未找到 Agent 授权路由")
		return
	}
	if !s.requireCSRF(w, r, ctx.Session) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxStockBodyBytes)
	var req struct {
		Reason string `json:"reason"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	switch parts[1] {
	case "approve":
		result, err := s.stockService().ApproveAgentAuthorization(r.Context(), parts[0])
		if err != nil {
			writeError(w, http.StatusBadRequest, "stock_agent_authorization_approve_failed", err.Error())
			return
		}
		_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
			EventType: "stock.agent_authorization.approved",
			RiskLevel: "high",
			Summary:   "已确认执行股票外部 Agent",
			Payload:   map[string]any{"authorizationId": result.Authorization.ID, "runId": result.Authorization.RunID, "profileId": result.Authorization.ProfileID, "status": result.Authorization.Status},
		})
		writeJSON(w, http.StatusOK, result)
	case "deny":
		result, err := s.stockService().DenyAgentAuthorization(r.Context(), parts[0], req.Reason)
		if err != nil {
			writeError(w, http.StatusBadRequest, "stock_agent_authorization_deny_failed", err.Error())
			return
		}
		_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
			EventType: "stock.agent_authorization.denied",
			RiskLevel: "medium",
			Summary:   "已拒绝执行股票外部 Agent",
			Payload:   map[string]any{"authorizationId": result.Authorization.ID, "runId": result.Authorization.RunID, "profileId": result.Authorization.ProfileID},
		})
		writeJSON(w, http.StatusOK, result)
	default:
		writeError(w, http.StatusNotFound, "stock_agent_authorization_action_not_found", "未找到 Agent 授权动作")
	}
}

func (s *Server) handleStockAgentLedgerCleanup(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	if !s.requireCSRF(w, r, ctx.Session) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxStockBodyBytes)
	var req struct {
		RetentionDays int `json:"retentionDays"`
		KeepRuns      int `json:"keepRuns"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	result, err := s.stockService().CleanupAgentLedger(r.Context(), req.RetentionDays, req.KeepRuns)
	if err != nil {
		writeError(w, http.StatusBadRequest, "stock_agent_ledger_cleanup_failed", err.Error())
		return
	}
	_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
		EventType: "stock.agent_ledger.cleaned",
		RiskLevel: "medium",
		Summary:   "已清理股票 Agent Decision Ledger 旧记录",
		Payload:   map[string]any{"runsDeleted": result.RunsDeleted, "stepsDeleted": result.StepsDeleted, "claimsDeleted": result.ClaimsDeleted, "authorizationsDeleted": result.AuthorizationsDeleted, "retentionDays": result.RetentionDays, "keepRuns": result.KeepRuns},
	})
	writeJSON(w, http.StatusOK, map[string]any{"result": result})
}

func (s *Server) handleStockAgentSteps(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuth(w, r); !ok {
		return
	}
	items, err := s.store.ListStockAgentRunSteps(r.Context(), r.URL.Query().Get("runId"), parseInt(r.URL.Query().Get("limit")))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "stock_agent_steps_read_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleStockAgentClaims(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuth(w, r); !ok {
		return
	}
	items, err := s.store.ListStockAgentClaims(r.Context(), r.URL.Query().Get("runId"), parseInt(r.URL.Query().Get("limit")))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "stock_agent_claims_read_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleStockStrategyPatches(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuth(w, r); !ok {
		return
	}
	items, err := s.store.ListStockStrategyPatches(r.Context(), r.URL.Query().Get("status"), parseInt(r.URL.Query().Get("limit")))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "stock_strategy_patches_read_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleStockStrategyPatchSubroutes(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/stock/strategy-patches/"), "/")
	parts := strings.Split(path, "/")
	if len(parts) != 2 || r.Method != http.MethodPost {
		writeError(w, http.StatusNotFound, "stock_strategy_patch_route_not_found", "未找到策略补丁路由")
		return
	}
	if !s.requireCSRF(w, r, ctx.Session) {
		return
	}
	switch parts[1] {
	case "accept":
		strategy, patch, err := s.stockService().AcceptStrategyPatch(r.Context(), parts[0])
		if err != nil {
			writeError(w, http.StatusBadRequest, "stock_strategy_patch_accept_failed", err.Error())
			return
		}
		_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
			EventType: "stock.strategy_patch.accepted",
			RiskLevel: "high",
			Summary:   "已接受股票策略补丁",
			Payload:   map[string]any{"patchId": patch.ID, "strategyId": strategy.ID, "newVersion": strategy.CurrentVersion},
		})
		writeJSON(w, http.StatusOK, map[string]any{"strategy": strategy, "patch": patch})
	case "reject":
		patch, err := s.stockService().RejectStrategyPatch(r.Context(), parts[0])
		if err != nil {
			writeError(w, http.StatusBadRequest, "stock_strategy_patch_reject_failed", err.Error())
			return
		}
		_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
			EventType: "stock.strategy_patch.rejected",
			RiskLevel: "medium",
			Summary:   "已拒绝股票策略补丁",
			Payload:   map[string]any{"patchId": patch.ID, "strategyId": patch.StrategyID},
		})
		writeJSON(w, http.StatusOK, map[string]any{"patch": patch})
	default:
		writeError(w, http.StatusNotFound, "stock_strategy_patch_route_not_found", "未找到策略补丁动作")
	}
}

func (s *Server) handleStockProposedOperations(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuth(w, r); !ok {
		return
	}
	items, err := s.store.ListStockProposedOperations(r.Context(), r.URL.Query().Get("status"), parseInt(r.URL.Query().Get("limit")))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "stock_proposals_read_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleStockProposedOperationSubroutes(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/stock/proposed-operations/"), "/")
	parts := strings.Split(path, "/")
	if len(parts) != 2 || (parts[1] != "confirm" && parts[1] != "cancel") || r.Method != http.MethodPost {
		writeError(w, http.StatusNotFound, "stock_proposal_route_not_found", "未找到操作建议路由")
		return
	}
	if !s.requireCSRF(w, r, ctx.Session) {
		return
	}
	if parts[1] == "cancel" {
		var req struct {
			Reason string `json:"reason"`
		}
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, maxStockBodyBytes)
			if ok, err := decodeOptionalJSON(w, r, &req); err != nil {
				return
			} else if !ok {
				return
			}
		}
		op, err := s.store.CancelStockProposedOperation(r.Context(), parts[0], req.Reason)
		if err != nil {
			writeError(w, http.StatusBadRequest, "stock_operation_cancel_failed", err.Error())
			return
		}
		_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
			EventType: "stock.operation.cancelled",
			RiskLevel: "high",
			Summary:   "已作废股票操作建议",
			Payload:   map[string]any{"proposalId": op.ID, "portfolioId": op.PortfolioID, "symbol": op.Symbol, "action": op.Action, "reason": req.Reason},
		})
		writeJSON(w, http.StatusOK, map[string]any{"operation": op})
		return
	}
	var req struct {
		Price                float64 `json:"price"`
		Quantity             float64 `json:"quantity"`
		Notes                string  `json:"notes"`
		RiskAcknowledged     bool    `json:"riskAcknowledged"`
		ExpectedAction       string  `json:"expectedAction"`
		ExpectedSymbol       string  `json:"expectedSymbol"`
		ExpectedGuardrail    string  `json:"expectedGuardrail"`
		ExpectedRiskSummary  string  `json:"expectedRiskSummary"`
		ConfirmedReferenceAt string  `json:"confirmedReferenceAt"`
		MaxQuoteAgeSeconds   int     `json:"maxQuoteAgeSeconds"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxStockBodyBytes)
	if !decodeJSON(w, r, &req) {
		return
	}
	operation, err := s.store.ConfirmStockProposedOperationWithCheck(r.Context(), parts[0], storage.StockOperationConfirmation{
		Price:                req.Price,
		Quantity:             req.Quantity,
		Notes:                req.Notes,
		RiskAcknowledged:     req.RiskAcknowledged,
		ExpectedAction:       req.ExpectedAction,
		ExpectedSymbol:       req.ExpectedSymbol,
		ExpectedGuardrail:    req.ExpectedGuardrail,
		ExpectedRiskSummary:  req.ExpectedRiskSummary,
		ConfirmedReferenceAt: req.ConfirmedReferenceAt,
		MaxQuoteAgeSeconds:   req.MaxQuoteAgeSeconds,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, "stock_operation_confirm_failed", err.Error())
		return
	}
	_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
		EventType: "stock.operation.confirmed",
		RiskLevel: "critical",
		Summary:   "已确认股票人工操作记录",
		Payload:   map[string]any{"operationId": operation.ID, "portfolioId": operation.PortfolioID, "symbol": operation.Symbol, "action": operation.Action, "quantity": operation.Quantity, "amount": operation.Amount},
	})
	writeJSON(w, http.StatusCreated, map[string]any{"operation": operation})
}

func decodeOptionalJSON(w http.ResponseWriter, r *http.Request, dest any) (bool, error) {
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(dest); err != nil {
		if errors.Is(err, io.EOF) {
			return true, nil
		}
		writeError(w, http.StatusBadRequest, "invalid_json", "JSON 请求体无效")
		return false, err
	}
	return true, nil
}

func (s *Server) handleStockOperations(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuth(w, r); !ok {
		return
	}
	items, err := s.store.ListStockOperations(r.Context(), parseInt(r.URL.Query().Get("limit")))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "stock_operations_read_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// -------------- 股票模块级配置 + 代理 + setup 教程 --------------

// handleGetStockSettings 获取股票模块设置（代理、TTL、刷新间隔等）。
func (s *Server) handleGetStockSettings(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuth(w, r); !ok {
		return
	}
	settings, err := s.stock.GetSettings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"settings": settings})
}

// handleUpdateStockSettings 保存股票模块设置（仅变更字段为必填，全部字段都会按
// 请求覆盖保存，语义等价于 PUT 整个 settings 资源）。
func (s *Server) handleUpdateStockSettings(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, ctx.Session) {
		return
	}
	current, err := s.stock.GetSettings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	// 全量请求体：每个字段都是 *T，前端可以只传要变更的字段，其余保留原值。
	var req struct {
		ProxyEnabled         *bool   `json:"proxyEnabled"`
		ProxyType            *string `json:"proxyType"`
		ProxyAddress         *string `json:"proxyAddress"`
		ProxyUseForEastmoney *bool   `json:"proxyUseForEastmoney"`
		ProxyUseForSina      *bool   `json:"proxyUseForSina"`
		ProxyUseForTencent   *bool   `json:"proxyUseForTencent"`
		QuoteTTLSeconds      *int    `json:"quoteTtlSeconds"`
		AutoRefreshEnabled   *bool   `json:"autoRefreshEnabled"`
		RefreshIntervalSecs  *int    `json:"refreshIntervalSecs"`
		DefaultDataSource    *string `json:"defaultDataSource"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	settings := current
	if req.ProxyEnabled != nil {
		settings.ProxyEnabled = *req.ProxyEnabled
	}
	if req.ProxyType != nil {
		settings.ProxyType = strings.TrimSpace(*req.ProxyType)
	}
	if req.ProxyAddress != nil {
		settings.ProxyAddress = strings.TrimSpace(*req.ProxyAddress)
	}
	if req.ProxyUseForEastmoney != nil {
		settings.ProxyUseForEastmoney = *req.ProxyUseForEastmoney
	}
	if req.ProxyUseForSina != nil {
		settings.ProxyUseForSina = *req.ProxyUseForSina
	}
	if req.ProxyUseForTencent != nil {
		settings.ProxyUseForTencent = *req.ProxyUseForTencent
	}
	if req.QuoteTTLSeconds != nil {
		settings.QuoteTTLSeconds = *req.QuoteTTLSeconds
	}
	if req.AutoRefreshEnabled != nil {
		settings.AutoRefreshEnabled = *req.AutoRefreshEnabled
	}
	if req.RefreshIntervalSecs != nil {
		settings.RefreshIntervalSecs = *req.RefreshIntervalSecs
	}
	if req.DefaultDataSource != nil {
		settings.DefaultDataSource = strings.TrimSpace(*req.DefaultDataSource)
	}
	if err := s.stock.ValidateSettings(settings); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_settings", err.Error())
		return
	}
	updated, err := s.stock.UpdateSettings(r.Context(), settings)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
		EventType: "stock.settings.updated",
		RiskLevel: "medium",
		Summary:   "已更新股票模块设置",
		Payload: map[string]any{
			"proxyEnabled":         updated.ProxyEnabled,
			"proxyType":            updated.ProxyType,
			"proxyAddress":         maskHostPort(updated.ProxyAddress),
			"proxyUseForEastmoney": updated.ProxyUseForEastmoney,
			"proxyUseForSina":      updated.ProxyUseForSina,
			"proxyUseForTencent":   updated.ProxyUseForTencent,
			"defaultDataSource":    updated.DefaultDataSource,
		},
	})
	writeJSON(w, http.StatusOK, map[string]any{"settings": updated})
}

// handleTestStockProxyConnectivity 按源逐个测试代理连通性，后端会真正发起一次最小化请求。
// body: {"sources":["eastmoney","sina","tencent"]}
func (s *Server) handleTestStockProxyConnectivity(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, ctx.Session) {
		return
	}
	var req struct {
		Sources []string `json:"sources"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if len(req.Sources) == 0 {
		req.Sources = []string{"eastmoney", "sina", "tencent"}
	}
	type result struct {
		Source    string `json:"source"`
		Ok        bool   `json:"ok"`
		LatencyMs int64  `json:"latencyMs,omitempty"`
		Status    int    `json:"status,omitempty"`
		Error     string `json:"error,omitempty"`
	}
	results := make([]result, 0, len(req.Sources))
	for _, src := range req.Sources {
		src = strings.ToLower(strings.TrimSpace(src))
		item := result{Source: src}
		latency, status, err := s.stock.TestProxyConnectivity(r.Context(), src)
		item.LatencyMs = latency
		item.Status = status
		if err != nil {
			item.Error = err.Error()
		} else if status != 0 && (status < 200 || status >= 300) {
			item.Error = fmt.Sprintf("HTTP %d", status)
		} else {
			item.Ok = true
		}
		results = append(results, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

// handleStockSetupGuide 返回一份 Markdown 教程（在前端弹窗里渲染），
// 说明如何在国内服务器搭建 Squid 正向代理并做好安全白名单。
func (s *Server) handleStockSetupGuide(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuth(w, r); !ok {
		return
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	_, _ = io.WriteString(w, stockSetupGuideMarkdown)
}

// maskHostPort 审计日志里给代理地址脱敏：保留首/尾字符，中间打星号。
// 纯 IP 场景（没有域名/身份信息）也做一次以防万一。
func maskHostPort(addr string) string {
	if addr == "" {
		return ""
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "***"
	}
	if len(host) <= 4 {
		host = "*.*.*.*"
	} else if strings.Count(host, ".") == 3 {
		// IPv4: 保留第一个 octet
		parts := strings.Split(host, ".")
		parts[1] = "*"
		parts[2] = "*"
		parts[3] = "*"
		host = strings.Join(parts, ".")
	} else {
		host = host[:2] + "***" + host[len(host)-1:]
	}
	return host + ":" + port
}

// stockSetupGuideMarkdown 代理搭建教程。Markdown 里的反引号用 bq 变量拼接，
// 因为 Go raw string literal 不允许包含反引号。
var stockSetupGuideMarkdown = buildStockSetupGuide()

func buildStockSetupGuide() string {
	const bq = "`"
	const bq3 = "```"
	var b strings.Builder
	b.Grow(8192)
	w := func(s ...string) {
		for _, x := range s {
			b.WriteString(x)
		}
		b.WriteByte('\n')
	}

	w("# 在国内服务器搭建 Squid 正向代理（供股票模块使用）")
	w()
	w("> 适用于：Phantom Lancer 部署在海外（如韩国、新加坡），访问东财 / 新浪 / 腾讯行情接口被墙时。")
	w("> 思路：在国内一台有公网 IP 的轻量服务器上跑 **Squid** 正向代理，只允许")
	w("> Phantom Lancer 服务器的公网出口 IP 访问，且目标域名严格白名单，避免成为公开代理。")
	w()
	w("---")
	w()
	w("## 一、准备工作")
	w()
	w("| 项目 | 说明 |")
	w("| --- | --- |")
	w("| 国内服务器 | 任意云厂商 1C1G 即可，CentOS / Debian / Ubuntu 都行 |")
	w("| 公网 IP | 记为 **", bq, "PROXY_IP", bq, "**（例如 ", bq, "12.34.56.78", bq, "） |")
	w("| 代理端口 | 自己定一个，建议 **", bq, "31280", bq, "**（默认 3128，改高段更安全） |")
	w("| Phantom Lancer 出口 IP | 记为 **", bq, "KR_SERVER_IP", bq, "**（例如 ", bq, "43.155.134.182", bq, "，登录 KR 服务器跑 ", bq, "curl -4 ifconfig.me", bq, " 查看） |")
	w("| 云厂商安全组 | **放行 TCP 31280** 入方向，源 IP 只能是 ", bq, "KR_SERVER_IP", bq, "（别放 0.0.0.0/0！） |")
	w("| 防火墙（iptables/firewalld/ufw） | 同样只允许 ", bq, "KR_SERVER_IP", bq, " 访问 31280 |")
	w()
	w("---")
	w()
	w("## 二、安装 Squid")
	w()
	w("### Debian / Ubuntu")
	w(bq3, "bash")
	w("sudo apt-get update")
	w("sudo apt-get install -y squid")
	w("sudo systemctl enable squid")
	w(bq3)
	w()
	w("### CentOS / Rocky / RHEL")
	w(bq3, "bash")
	w("sudo yum install -y squid")
	w("sudo systemctl enable squid")
	w(bq3)
	w()
	w("---")
	w()
	w("## 三、写配置文件 ", bq, "/etc/squid/squid.conf", bq)
	w()
	w("**注意**：Squid 的 ACL 匹配顺序是 **先到先得**，所以「拒绝非白名单域名」")
	w("必须写在「允许韩国服务器 IP」之前，否则白名单不生效！")
	w()
	w(bq3, "conf")
	w("# ========== 基础 ==========")
	w("http_port 31280")
	w("visible_hostname stock-proxy")
	w("via off")
	w("forwarded_for delete")
	w("request_header_access X-Forwarded-For deny all")
	w("request_header_access Via deny all")
	w()
	w("# DNS 解析用国内公共 DNS")
	w("dns_nameservers 223.5.5.5 119.29.29.29")
	w()
	w("# 日志（可选，默认路径已够）")
	w("# access_log /var/log/squid/access.log squid")
	w()
	w("# ========== 访问来源 ACL ==========")
	w("# 把下面替换成你自己的 Phantom Lancer 出口 IP")
	w("acl kr_server src 43.155.134.182/32")
	w()
	w("# ========== 目标域名白名单 ==========")
	w("# 注意（2026-06）：东方财富 push2.eastmoney.com / push2his.eastmoney.com 在所有云厂商出口 IP")
	w("# 均存在应用层 RST（即便是 Squid CONNECT 代理转发），Phantom Lancer 代码已完全下线东财所有接口，")
	w("# 因此不再需要 eastmoney.com 系列域名，以下仅保留实际使用的 sina + qt.gtimg.cn。")
	w()
	w("# 新浪行情 / 主数据")
	w("acl allowed_domains dstdomain .sina.com.cn")
	w("acl allowed_domains dstdomain money.finance.sina.com.cn")
	w("acl allowed_domains dstdomain hq.sinajs.cn")
	w()
	w("# 腾讯行情（实时行情批量 + 代理连通性测试探针）")
	w("acl allowed_domains dstdomain qt.gtimg.cn")
	w("acl allowed_domains dstdomain .gtimg.cn")
	w()
	w("# ========== HTTP 访问控制（顺序非常重要！）==========")
	w("# ① 先拒绝一切不在目标白名单里的域名（这是防滥用的核心）")
	w("http_access deny !allowed_domains")
	w()
	w("# ② 再放行来自韩国服务器的合法请求")
	w("http_access allow kr_server")
	w()
	w("# ③ 最后拒绝其他所有来源（兜底）")
	w("http_access deny all")
	w(bq3)
	w()
	w("### 检查配置语法")
	w(bq3, "bash")
	w("sudo squid -k parse")
	w(bq3)
	w("看到末尾 ", bq, "... OK", bq, " 就代表没问题；WARNING 基本不影响，但 ERROR 必须修。")
	w()
	w("### 重启生效")
	w(bq3, "bash")
	w("sudo systemctl restart squid")
	w("sudo systemctl status squid")
	w(bq3)
	w()
	w("---")
	w()
	w("## 四、验证代理可用")
	w()
	w("在 **Phantom Lancer 所在服务器（KR）** 上执行以下三条命令：")
	w()
	w(bq3, "bash")
	w("# 1) 新浪 - 应该返回 HTTP 200 + JSON（universe 主源）")
	w(`curl -x http://PROXY_IP:31280 \`)
	w(`  -s -o /tmp/sina.json -w "sina: http=%{http_code} size=%{size_download}\n" \`)
	w(`  -H 'Referer: https://finance.sina.com.cn/' \`)
	w(`  -H 'User-Agent: Mozilla/5.0' \`)
	w(`  'https://money.finance.sina.com.cn/quotes_service/api/json_v2.php/Market_Center.getHQNodeData?page=1&num=2&sort=symbol&asc=1&node=sh_a'`)
	w()
	w("# 2) 腾讯 qt - 应该返回 HTTP 200（实时行情 + 代理连通性测试探针接口）")
	w(`curl -x http://PROXY_IP:31280 \`)
	w(`  -s -o /tmp/qq.txt -w "tencent: http=%{http_code} size=%{size_download}\n" \`)
	w(`  -H 'User-Agent: Mozilla/5.0' \`)
	w(`  'https://qt.gtimg.cn/q=sh600000'`)
	w()
	w("# 3) 负向验证 - 百度必须被 ACL 拒绝（CONNECT 403 / 000，证明白名单生效）")
	w(`curl -x http://PROXY_IP:31280 -sS --max-time 5 \`)
	w(`  -o /tmp/baidu.txt -w "baidu_deny: http=%{http_code} connect=%{connect_code}\n" \`)
	w(`  'https://www.baidu.com/'`)
	w(bq3)
	w()
	w("### 关键的负向验证（白名单是否生效）")
	w(bq3, "bash")
	w("# 访问代理白名单外的网站 - 应该得到 HTTP 403（Access Denied）")
	w(`curl -x http://PROXY_IP:31280 -s -w "baidu: http=%{http_code}\n" -o /dev/null \`)
	w(`  -H 'User-Agent: Mozilla/5.0' 'https://www.baidu.com'`)
	w(bq3)
	w("如果这条不是 **403**，说明 ACL 顺序写错了（", bq, "allow kr_server", bq, " 放得太靠前），")
	w("请翻回第三节检查顺序！")
	w()
	w("---")
	w()
	w("## 五、在 Phantom Lancer 前端填入配置")
	w()
	w("1. 打开 **股票 → Settings** Tab")
	w("2. **代理总开关** → 打开")
	w("3. **Proxy 类型** → 选择 ", bq, "http", bq)
	w("4. **代理地址** → 填 ", bq, "PROXY_IP:31280", bq, "（不含 http://）")
	w("5. 勾选 **Eastmoney / Sina / Tencent** 里你想走代理的源（建议全开）")
	w("6. 点「测试代理连通性」 — 应该看到三条都是 **OK**")
	w("7. 点「保存」，完成")
	w()
	w("保存后所有股票相关的接口请求都会按你勾选的源自动走代理，其他系统模块不受影响。")
	w()
	w("---")
	w()
	w("## 六、日常运维（可选）")
	w()
	w("### 查看访问日志")
	w(bq3, "bash")
	w(`sudo tail -f /var/log/squid/access.log | grep -v 'TCP_DENIED'`)
	w(bq3)
	w()
	w("### 只允许 KR 出口 IP（云厂商安全组层面再锁一层）")
	w("云控制台 → 安全组 → 入方向 TCP 31280，源写死 ", bq, "KR_SERVER_IP/32", bq, "。")
	w("这样即使 Squid 配置错了，别的 IP 根本连不上端口。")
	w()
	w("### 改了配置后只重载，不重启")
	w(bq3, "bash")
	w("sudo squid -k reconfigure")
	w(bq3)
	w()
	return b.String()
}

