package httpapi

import (
	"context"
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
