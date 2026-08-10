package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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
