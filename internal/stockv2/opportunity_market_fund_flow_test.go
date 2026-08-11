package stockv2

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

func TestParseTushareMoneyFlow(t *testing.T) {
	fields := []string{"ts_code", "trade_date", "buy_sm_amount", "sell_sm_amount", "buy_md_amount", "sell_md_amount", "buy_lg_amount", "sell_lg_amount", "buy_elg_amount", "sell_elg_amount", "net_mf_amount"}
	points, err := parseTushareMoneyFlow(fields, [][]any{{"000001.SZ", "20260810", 1.0, 2.0, 3.0, 4.0, 5.0, 6.0, 7.0, 8.0, 9.0}})
	if err != nil || len(points) != 1 {
		t.Fatalf("points=%+v err=%v", points, err)
	}
	if points[0].MainNet != 90000 || points[0].Turnover != 360000 || points[0].TradeDate != "2026-08-10" {
		t.Fatalf("point=%+v", points[0])
	}
}

func TestRequestTushareMoneyFlowRejectsBusinessErrorWithoutLeakingKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != "test-secret" {
			t.Error("missing API key header")
		}
		_, _ = w.Write([]byte(`{"code":40203,"msg":"rate limited","data":{"fields":[],"items":[]}}`))
	}))
	defer server.Close()
	_, err := requestTushareMoneyFlow(context.Background(), server.Client(), server.URL, "test-secret", nil)
	if err == nil || !strings.Contains(err.Error(), "40203") || strings.Contains(err.Error(), "test-secret") {
		t.Fatalf("error=%v", err)
	}
}

func TestOpportunityMarketScanConfigDoesNotSerializeSecrets(t *testing.T) {
	config := OpportunityMarketScanConfig{PrimaryFundFlowAPIKey: "primary-secret", BackupFundFlowAPIKey: "backup-secret", BackupFundFlowProxy: "http://user:pass@example.invalid"}
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, secret := range []string{"primary-secret", "backup-secret", "user:pass"} {
		if strings.Contains(text, secret) {
			t.Fatalf("serialized secret %q: %s", secret, text)
		}
	}
}

func TestOpportunityFundFlowCoverageBoundaryAndPercentiles(t *testing.T) {
	candidates := make([]OpportunityMarketScanCandidate, 30)
	for i := range candidates {
		candidates[i].Metrics.FundFlowAvailable = i < 24
		candidates[i].Metrics.MainFlowRatio20 = float64(i)
		candidates[i].Metrics.PositiveFlowDays20 = i
	}
	available := 0
	for i := range candidates {
		if candidates[i].Metrics.FundFlowAvailable {
			available++
		}
	}
	used := float64(available)/float64(len(candidates)) >= opportunityMarketScanMinimumCoverage
	if !used {
		t.Fatal("24/30 must satisfy the 80% coverage gate")
	}
	scoreOpportunityFundFlowPercentiles(candidates)
	if candidates[23].FlowScore <= candidates[0].FlowScore || candidates[24].FlowScore != 0 {
		t.Fatalf("unexpected percentile scores: first=%v last=%v unavailable=%v", candidates[0].FlowScore, candidates[23].FlowScore, candidates[24].FlowScore)
	}
}

func TestOpportunityFundFlowBackupProxyValidation(t *testing.T) {
	if _, err := opportunityFundFlowBackupClient("socks5://example.invalid:1080"); err == nil {
		t.Fatal("unsupported proxy scheme accepted")
	}
}

func TestOpportunityFundFlowRetriesThenUsesBackup(t *testing.T) {
	var primaryCalls, backupCalls atomic.Int32
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		primaryCalls.Add(1)
		http.Error(w, "busy", http.StatusTooManyRequests)
	}))
	defer primary.Close()
	backup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		backupCalls.Add(1)
		_, _ = w.Write([]byte(`{"code":0,"msg":"ok","data":{"fields":["trade_date","net_mf_amount","buy_sm_amount","sell_sm_amount","buy_md_amount","sell_md_amount","buy_lg_amount","sell_lg_amount","buy_elg_amount","sell_elg_amount"],"items":[["20260810",1,1,1,1,1,1,1,1,1]]}}`))
	}))
	defer backup.Close()
	result, err := fetchOpportunityFundFlowSources(context.Background(), []opportunityFundFlowSource{
		{Name: "primary", Endpoint: primary.URL, Key: "a", Client: primary.Client()},
		{Name: "backup", Endpoint: backup.URL, Key: "b", Client: backup.Client()},
	}, url.Values{"ts_code": {"000001.SZ"}})
	if err != nil || result.Source != "backup" || primaryCalls.Load() != 2 || backupCalls.Load() != 1 {
		t.Fatalf("result=%+v err=%v primary=%d backup=%d", result, err, primaryCalls.Load(), backupCalls.Load())
	}
}
