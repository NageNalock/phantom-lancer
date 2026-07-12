package httpapi

import (
	"net/http"
	"testing"

	"phantom-lancer/internal/stockv2"
)

func TestStockV2AgentHTTPStatusMapsReadinessBlockToConflict(t *testing.T) {
	err := &stockv2.StrategyGenerationReadinessError{
		Decision: stockv2.AssetReadinessDecision{Status: stockv2.AssetReadinessDecisionBlocked},
	}
	if got := stockV2AgentHTTPStatus(err); got != http.StatusConflict {
		t.Fatalf("status = %d, want %d", got, http.StatusConflict)
	}
}
