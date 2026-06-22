package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStockV2SensitiveWritesRequireCSRF(t *testing.T) {
	server, _, session, _ := newStockHTTPTest(t)

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
