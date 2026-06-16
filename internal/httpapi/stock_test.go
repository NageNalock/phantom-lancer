package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"phantom-lancer/internal/auth"
	"phantom-lancer/internal/storage"
)

func TestStockWatchCheckRejectsInvalidJSONWithoutSideEffects(t *testing.T) {
	server, store, session, csrf := newStockHTTPTest(t)
	ctx := context.Background()
	strategy, err := store.CreateStockStrategy(ctx, storage.StockStrategy{
		Title:             "invalid json watch",
		StrategyType:      "account_agnostic",
		Symbol:            "600519",
		TriggerPriceAbove: 10,
	})
	if err != nil {
		t.Fatalf("create strategy: %v", err)
	}
	if _, err := store.CreateStockWatch(ctx, storage.StockWatch{StrategyID: strategy.ID}); err != nil {
		t.Fatalf("create watch: %v", err)
	}
	if _, err := store.UpsertStockQuote(ctx, storage.StockQuote{
		Symbol:         strategy.Symbol,
		LastPrice:      11,
		DataTimestamp:  time.Now().UTC().Format(time.RFC3339Nano),
		DataFreshness:  "fresh",
		TradableStatus: "tradable",
	}); err != nil {
		t.Fatalf("upsert quote: %v", err)
	}

	recorder := httptest.NewRecorder()
	request := newStockRequest(http.MethodPost, "/api/stock/watches/check", "{", session, csrf)
	server.handleStockWatchCheck(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
	alerts, err := store.ListStockAlerts(ctx, "", 10)
	if err != nil {
		t.Fatalf("list alerts: %v", err)
	}
	if len(alerts) != 0 {
		t.Fatalf("invalid JSON created alerts: %+v", alerts)
	}
}

func TestStockPortfolioDeleteRouteDeletesEmptyAccount(t *testing.T) {
	server, store, session, csrf := newStockHTTPTest(t)
	ctx := context.Background()
	portfolio, err := store.CreateStockPortfolio(ctx, storage.StockPortfolio{Name: "delete me", Cash: 1000})
	if err != nil {
		t.Fatalf("create portfolio: %v", err)
	}
	if _, err := store.UpsertStockHolding(ctx, storage.StockHolding{PortfolioID: portfolio.ID, Symbol: "600519", Quantity: 100}); err != nil {
		t.Fatalf("upsert holding: %v", err)
	}

	recorder := httptest.NewRecorder()
	request := newStockRequest(http.MethodDelete, "/api/stock/portfolios/"+portfolio.ID, "", session, csrf)
	server.handleStockPortfolioSubroutes(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Deleted storage.StockPortfolioDeleteImpact `json:"deleted"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.Deleted.Portfolio.ID != portfolio.ID || payload.Deleted.HoldingsDeleted != 1 {
		t.Fatalf("deleted payload = %+v, want portfolio and holding count", payload.Deleted)
	}
	if _, err := store.GetStockPortfolio(ctx, portfolio.ID); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("get deleted portfolio err = %v, want ErrNotFound", err)
	}
}

func TestStockPortfolioPatchRouteUpdatesConfigAndCash(t *testing.T) {
	server, store, session, csrf := newStockHTTPTest(t)
	ctx := context.Background()
	portfolio, err := store.CreateStockPortfolio(ctx, storage.StockPortfolio{Name: "before", Cash: 1000})
	if err != nil {
		t.Fatalf("create portfolio: %v", err)
	}

	recorder := httptest.NewRecorder()
	request := newStockRequest(http.MethodPatch, "/api/stock/portfolios/"+portfolio.ID, `{
		"name": "after",
		"cashDelta": -125.5,
		"riskLevel": "aggressive",
		"maxSinglePositionPct": 0.35,
		"maxDrawdownPct": 0.22,
		"allowBuy": false,
		"allowAdd": false,
		"allowReduce": true,
		"allowSell": true
	}`, session, csrf)
	server.handleStockPortfolioSubroutes(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Portfolio storage.StockPortfolio `json:"portfolio"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.Portfolio.Name != "after" || payload.Portfolio.Cash != 874.5 {
		t.Fatalf("portfolio response = %+v, want updated name and cash", payload.Portfolio)
	}
	if payload.Portfolio.RiskLevel != "aggressive" || payload.Portfolio.MaxSinglePositionPct != 0.35 || payload.Portfolio.MaxDrawdownPct != 0.22 {
		t.Fatalf("portfolio guardrails not updated: %+v", payload.Portfolio)
	}
	if payload.Portfolio.AllowBuy || payload.Portfolio.AllowAdd || !payload.Portfolio.AllowReduce || !payload.Portfolio.AllowSell {
		t.Fatalf("portfolio permissions not updated: %+v", payload.Portfolio)
	}
	audits, err := store.ListAudit(ctx, 5)
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}
	if len(audits) == 0 || audits[0].EventType != "stock.portfolio.updated" {
		t.Fatalf("latest audit = %+v, want stock.portfolio.updated", audits)
	}
}

func TestStockInstrumentSearchHonorsListingDateAlias(t *testing.T) {
	server, store, session, _ := newStockHTTPTest(t)
	ctx := context.Background()
	if _, err := store.UpsertStockInstrument(ctx, storage.StockInstrument{Symbol: "600000", Market: "SH", Name: "浦发银行", Status: "listed", ListingDate: "1999-11-10"}); err != nil {
		t.Fatalf("upsert old instrument: %v", err)
	}
	if _, err := store.UpsertStockInstrument(ctx, storage.StockInstrument{Symbol: "300750", Market: "SZ", Name: "宁德时代", Status: "listed", ListingDate: "2018-06-11"}); err != nil {
		t.Fatalf("upsert new instrument: %v", err)
	}

	recorder := httptest.NewRecorder()
	request := newStockRequest(http.MethodGet, "/api/stock/instruments/search?min_listing_date=2010-01-01&pageSize=50", "", session, "")
	server.handleStockInstrumentSearch(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", recorder.Code, recorder.Body.String())
	}
	var payload storage.StockInstrumentSearchResult
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.Total != 1 || len(payload.Items) != 1 || payload.Items[0].Symbol != "300750" {
		t.Fatalf("payload = %+v, want only 300750", payload)
	}
}

func TestStockInstrumentListCanExcludeDelisted(t *testing.T) {
	server, store, session, _ := newStockHTTPTest(t)
	ctx := context.Background()
	if _, err := store.UpsertStockInstrument(ctx, storage.StockInstrument{Symbol: "600000", Market: "SH", Name: "浦发银行", Status: "listed"}); err != nil {
		t.Fatalf("upsert listed instrument: %v", err)
	}
	if _, err := store.UpsertStockInstrument(ctx, storage.StockInstrument{Symbol: "000001", Market: "SZ", Name: "平安银行", Status: "delisted"}); err != nil {
		t.Fatalf("upsert delisted instrument: %v", err)
	}

	recorder := httptest.NewRecorder()
	request := newStockRequest(http.MethodGet, "/api/stock/instruments?include_delisted=false&limit=10", "", session, "")
	server.handleStockInstruments(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Items []storage.StockInstrument `json:"items"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(payload.Items) != 1 || payload.Items[0].Symbol != "600000" {
		t.Fatalf("items = %+v, want only listed instrument", payload.Items)
	}
}

func newStockHTTPTest(t *testing.T) (*Server, *storage.Store, string, string) {
	t.Helper()
	ctx := context.Background()
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "phantom-lancer.db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	owner, err := store.CreateOwner(ctx, "owner", "hash-not-used")
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	sessionRaw, sessionHash, err := auth.NewToken()
	if err != nil {
		t.Fatalf("session token: %v", err)
	}
	csrfRaw, csrfHash, err := auth.NewToken()
	if err != nil {
		t.Fatalf("csrf token: %v", err)
	}
	if _, err := store.CreateSession(ctx, owner.ID, sessionHash, csrfHash, false, time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatalf("create session: %v", err)
	}
	return &Server{store: store}, store, sessionRaw, csrfRaw
}

func newStockRequest(method, target, body, session, csrf string) *http.Request {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", sessionCookie+"="+session)
	req.Header.Set("X-CSRF-Token", csrf)
	return req
}
