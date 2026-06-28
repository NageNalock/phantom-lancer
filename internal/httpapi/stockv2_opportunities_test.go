package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"phantom-lancer/internal/stockv2"
)

func TestStockV2OpportunityHTTPRoutes(t *testing.T) {
	_, svc, mux := newStockV2OpportunityHTTPTest(t)

	rec := stockV2OpportunityHTTPReq(t, mux, http.MethodPost, "/api/stockv2/opportunities", map[string]any{
		"title":      "AI model release",
		"userThesis": "AI model quality may create downstream stock opportunities",
		"createdBy":  "tester",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("create status=%d body=%s", rec.Code, rec.Body.String())
	}
	var opp stockv2.Opportunity
	if err := json.Unmarshal(rec.Body.Bytes(), &opp); err != nil {
		t.Fatalf("decode opportunity: %v", err)
	}
	if opp.ID == "" || opp.Status != stockv2.OpportunityStatusDraft {
		t.Fatalf("opportunity=%+v, want draft with id", opp)
	}

	rec = stockV2OpportunityHTTPReq(t, mux, http.MethodGet, "/api/stockv2/opportunities?limit=10&offset=0&q=AI", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", rec.Code, rec.Body.String())
	}
	var list struct {
		Items []stockv2.Opportunity `json:"items"`
		Total int                   `json:"total"`
		Limit int                   `json:"limit"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if list.Total != 1 || len(list.Items) != 1 || list.Limit != 10 {
		t.Fatalf("list=%+v, want one paged item", list)
	}

	model := stockV2OpportunityHTTPSeedChatModel(t, svc)
	modelID := model.ID
	if _, err := svc.UpdateAgentTaskProfile(context.Background(), stockv2.AgentTaskTypeOpportunityDiscovery, stockv2.RequestUpdateAgentTaskProfile{PrimaryModelID: &modelID}); err != nil {
		t.Fatalf("bind opportunity task profile: %v", err)
	}
	rec = stockV2OpportunityHTTPReq(t, mux, http.MethodPost, "/api/stockv2/opportunities/"+opp.ID+"/discovery-runs", map[string]any{
		"requestedBy": "tester",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("start run status=%d body=%s", rec.Code, rec.Body.String())
	}
	var run stockv2.OpportunityDiscoveryRun
	if err := json.Unmarshal(rec.Body.Bytes(), &run); err != nil {
		t.Fatalf("decode run: %v", err)
	}
	if run.OpportunityID != opp.ID || run.AgentRunID == "" || run.StepTotal == 0 {
		t.Fatalf("run=%+v, want run linked to opportunity and agent run", run)
	}

	rec = stockV2OpportunityHTTPReq(t, mux, http.MethodGet, "/api/stockv2/opportunity-discovery-runs/"+run.ID+"/steps?limit=20", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("steps status=%d body=%s", rec.Code, rec.Body.String())
	}
	var steps struct {
		Items []stockv2.OpportunityDiscoveryStep `json:"items"`
		Total int                                `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &steps); err != nil {
		t.Fatalf("decode steps: %v", err)
	}
	if steps.Total != run.StepTotal || len(steps.Items) != run.StepTotal {
		t.Fatalf("steps=%+v run=%+v, want default steps", steps, run)
	}

	rec = stockV2OpportunityHTTPReq(t, mux, http.MethodGet, "/api/stockv2/embeddings/status", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("embedding status status=%d body=%s", rec.Code, rec.Body.String())
	}
	var embeddingStatus stockv2.EmbeddingStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &embeddingStatus); err != nil {
		t.Fatalf("decode embedding status: %v", err)
	}
	if embeddingStatus.Code != stockv2.EmbeddingStatusModelNotConfigured {
		t.Fatalf("embedding status=%+v, want model_not_configured", embeddingStatus)
	}

	rec = stockV2OpportunityHTTPReq(t, mux, http.MethodPost, "/api/stockv2/embeddings/rebuild", map[string]any{})
	if rec.Code != http.StatusConflict {
		t.Fatalf("rebuild status=%d body=%s, want 409 when embedding model is unbound", rec.Code, rec.Body.String())
	}
}

func newStockV2OpportunityHTTPTest(t *testing.T) (*Server, *stockv2.Service, *http.ServeMux) {
	t.Helper()
	dir := t.TempDir()
	store, err := stockv2.NewStoreWithMarketDB(
		filepath.Join(dir, "stockv2.db"),
		filepath.Join(dir, "stock_market.duckdb"),
	)
	if err != nil {
		t.Fatalf("new stockv2 store: %v", err)
	}
	svc := stockv2.NewService(store, nil, nil)
	t.Cleanup(func() { _ = svc.Close() })
	server := &Server{stockV2: svc}
	mux := http.NewServeMux()
	server.RegisterStockV2Routes(mux)
	return server, svc, mux
}

func stockV2OpportunityHTTPReq(t *testing.T, mux http.Handler, method, target string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		payload, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(payload)
	}
	req := httptest.NewRequest(method, target, reader)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func stockV2OpportunityHTTPSeedChatModel(t *testing.T, svc *stockv2.Service) stockv2.AgentModelProfile {
	t.Helper()
	ctx := context.Background()
	provider, err := svc.CreateAgentProviderProfile(ctx, stockv2.RequestCreateAgentProviderProfile{
		ProviderType: stockv2.AgentProviderTypeCodexCLI,
		Name:         "codex-opportunity-http",
	})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	model, err := svc.CreateAgentModelProfile(ctx, stockv2.RequestCreateAgentModelProfile{
		ProviderID: provider.ID,
		ModelName:  "gpt-opportunity-http",
		Enabled:    true,
	})
	if err != nil {
		t.Fatalf("create model: %v", err)
	}
	return model
}
