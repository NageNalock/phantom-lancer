package httpapi

import (
	"net/http/httptest"
	"testing"

	"phantom-lancer/internal/stockv2"
)

func TestStockV2StrategyFilterArchivedVisibility(t *testing.T) {
	for _, tc := range []struct {
		name        string
		query       string
		wantStatus  string
		wantExclude bool
		wantErr     bool
	}{
		{name: "default keeps API compatibility"},
		{name: "current statuses", query: "?includeArchived=false", wantExclude: true},
		{name: "all statuses", query: "?includeArchived=true"},
		{name: "archived status", query: "?status=archived", wantStatus: stockv2.StrategyStatusArchived},
		{name: "invalid include archived", query: "?includeArchived=yes", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/api/stockv2/strategies"+tc.query, nil)
			filter, err := stockV2StrategyFilterFromRequest(req)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("parse filter: %v", err)
			}
			if filter.Status != tc.wantStatus || filter.ExcludeArchived != tc.wantExclude {
				t.Fatalf("filter = %+v, want status %q excludeArchived %v", filter, tc.wantStatus, tc.wantExclude)
			}
		})
	}
}
