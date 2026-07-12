package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"phantom-lancer/internal/stockv2"
)

func TestStockV2EvaluateAssetReadinessDeduplicatesWithoutTruncating(t *testing.T) {
	_, store, mux := newStockV2AssetReadinessHTTPTest(t)
	seedStockV2ReadinessInstruments(t, store, "000001", "600000")
	rec := stockV2AssetReadinessRequest(t, mux, http.MethodPost, "/api/stockv2/assets/readiness/evaluate", map[string]any{
		"symbols": []string{"SZ000001", "000001", "600000.SH"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var response struct {
		Items []stockv2.UnifiedAssetReadiness `json:"items"`
		Count int                             `json:"count"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Count != 2 || len(response.Items) != 2 || response.Items[0].Symbol != "000001" || response.Items[1].Symbol != "600000" {
		t.Fatalf("response = %+v", response)
	}
}

func TestStockV2EvaluateAssetReadinessRejectsMoreThanFiveHundredRawSymbols(t *testing.T) {
	_, _, mux := newStockV2AssetReadinessHTTPTest(t)
	symbols := make([]string, 501)
	for i := range symbols {
		symbols[i] = "000001"
	}
	rec := stockV2AssetReadinessRequest(t, mux, http.MethodPost, "/api/stockv2/assets/readiness/evaluate", map[string]any{"symbols": symbols})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var response struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Error.Code != "too_many_symbols" {
		t.Fatalf("error = %+v", response.Error)
	}
}

func TestStockV2AssetReadinessOverviewCoversStoredUniverse(t *testing.T) {
	_, store, mux := newStockV2AssetReadinessHTTPTest(t)
	seedStockV2ReadinessInstruments(t, store, "000001", "600000")
	rec := stockV2AssetReadinessRequest(t, mux, http.MethodGet, "/api/stockv2/assets/readiness/overview", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var overview stockv2.AssetReadinessOverview
	if err := json.Unmarshal(rec.Body.Bytes(), &overview); err != nil {
		t.Fatalf("decode overview: %v", err)
	}
	if overview.TargetCount != 2 || overview.EvaluatedCount != 2 {
		t.Fatalf("overview = %+v", overview)
	}
}

func newStockV2AssetReadinessHTTPTest(t *testing.T) (*Server, *stockv2.Store, *http.ServeMux) {
	t.Helper()
	dir := t.TempDir()
	store, err := stockv2.NewStoreWithMarketDB(filepath.Join(dir, "stockv2.db"), filepath.Join(dir, "stock_market.duckdb"))
	if err != nil {
		t.Fatalf("new stockv2 store: %v", err)
	}
	svc := stockv2.NewService(store, nil, nil)
	t.Cleanup(func() { _ = svc.Close() })
	server := &Server{stockV2: svc}
	mux := http.NewServeMux()
	server.RegisterStockV2Routes(mux)
	if err := store.UpsertObservedTradingDates(context.Background(), []string{"2026-07-10"}, time.Now()); err != nil {
		t.Fatalf("seed observed calendar: %v", err)
	}
	return server, store, mux
}

func seedStockV2ReadinessInstruments(t *testing.T, store *stockv2.Store, symbols ...string) {
	t.Helper()
	for _, symbol := range symbols {
		market := "SZ"
		if symbol[0] == '6' {
			market = "SH"
		}
		if err := store.UpsertInstrument(context.Background(), stockv2.StockV2Instrument{
			ID:             "inst-" + symbol,
			Symbol:         symbol,
			Market:         market,
			InstrumentType: stockv2.InstrumentTypeStock,
			Name:           "测试标的" + symbol,
		}); err != nil {
			t.Fatalf("upsert instrument %s: %v", symbol, err)
		}
	}
}

func stockV2AssetReadinessRequest(t *testing.T, handler http.Handler, method, target string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
	}
	req := httptest.NewRequest(method, target, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}
