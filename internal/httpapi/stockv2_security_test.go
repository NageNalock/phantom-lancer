package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"phantom-lancer/internal/stockv2"
)

func TestStockV2OpportunityDiscoveryScopeConfigRequiresOwnerAndPersists(t *testing.T) {
	server, _, session, csrf := newStockV2HTTPTest(t)

	req := httptest.NewRequest(http.MethodPatch, "/api/stockv2/opportunity-discovery/config", strings.NewReader(`{"excludeChiNextAndStarMarket":true}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", sessionCookie+"="+session)
	req.Header.Set("X-CSRF-Token", csrf)
	rec := httptest.NewRecorder()
	server.handleStockV2UpdateOpportunityDiscoveryConfig(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("update status=%d body=%s", rec.Code, rec.Body.String())
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/stockv2/opportunity-discovery/config", nil)
	getReq.Header.Set("Cookie", sessionCookie+"="+session)
	getRec := httptest.NewRecorder()
	server.handleStockV2GetOpportunityDiscoveryConfig(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("get status=%d body=%s", getRec.Code, getRec.Body.String())
	}
	var config stockv2.OpportunityDiscoveryConfig
	if err := json.Unmarshal(getRec.Body.Bytes(), &config); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	if !config.ExcludeChiNextAndStarMarket {
		t.Fatalf("config=%+v, want board exclusion enabled", config)
	}
}

func TestStockV2SensitiveWritesRequireCSRF(t *testing.T) {
	server, _, session, _ := newStockV2HTTPTest(t)

	for _, tc := range []struct {
		name   string
		method string
		target string
		body   string
		call   func(http.ResponseWriter, *http.Request)
	}{
		{
			name:   "settings update",
			method: http.MethodPut,
			target: "/api/stockv2/settings",
			body:   `{}`,
			call:   server.handleStockV2UpdateSettings,
		},
		{
			name:   "news source fetch",
			method: http.MethodPost,
			target: "/api/stockv2/news/sources/financialjuice/fetch",
			body:   ``,
			call:   server.handleStockV2FetchRawNewsSource,
		},
		{
			name:   "raw news truncate",
			method: http.MethodPost,
			target: "/api/stockv2/news/raw/truncate",
			body:   `{"before":"2026-06-24T00:00:00Z"}`,
			call:   server.handleStockV2TruncateRawNews,
		},
		{
			name:   "opportunity discovery scope config",
			method: http.MethodPatch,
			target: "/api/stockv2/opportunity-discovery/config",
			body:   `{"excludeChiNextAndStarMarket":true}`,
			call:   server.handleStockV2UpdateOpportunityDiscoveryConfig,
		},
		{
			name:   "opportunity market scan config",
			method: http.MethodPatch,
			target: "/api/stockv2/opportunity-market-scan/config",
			body:   `{"enabled":true}`,
			call:   server.handleStockV2UpdateOpportunityMarketScanConfig,
		},
		{
			name:   "opportunity market scan start",
			method: http.MethodPost,
			target: "/api/stockv2/opportunity-market-scan/runs",
			body:   `{}`,
			call:   server.handleStockV2StartOpportunityMarketScanRun,
		},
		{
			name:   "opportunity market scan retry",
			method: http.MethodPost,
			target: "/api/stockv2/opportunity-market-scan/runs/run-1/retry",
			body:   `{}`,
			call:   server.handleStockV2RetryOpportunityMarketScanRun,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.target, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Cookie", sessionCookie+"="+session)
			rec := httptest.NewRecorder()

			tc.call(rec, req)

			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
			}
		})
	}
}
