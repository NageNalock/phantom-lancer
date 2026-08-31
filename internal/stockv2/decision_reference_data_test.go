package stockv2

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestFetchDecisionTushareDatasetSourcesRetriesPrimaryAndBackup(t *testing.T) {
	primaryCalls, backupCalls := 0, 0
	primary := opportunityFundFlowSource{
		Name:     "primary",
		Endpoint: "https://primary.example/fina_indicator",
		Client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			primaryCalls++
			return stringResponse(http.StatusOK, `{"code":40203,"msg":"permission denied"}`), nil
		})},
	}
	backup := opportunityFundFlowSource{
		Name:     "backup",
		Endpoint: "https://backup.example/fina_indicator",
		Client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			backupCalls++
			if backupCalls == 1 {
				return nil, errors.New("proxyconnect tcp: CONNECT tunnel failed, response 407")
			}
			return stringResponse(http.StatusOK, `{"code":0,"data":{"fields":["ts_code"],"items":[["603228.SH"]]}}`), nil
		})},
	}
	result, err := fetchDecisionTushareDatasetSources(context.Background(), []opportunityFundFlowSource{primary, backup}, url.Values{"ts_code": {"603228.SH"}})
	if err != nil {
		t.Fatalf("fetch reference dataset: %v", err)
	}
	if primaryCalls != decisionReferenceAttempts || backupCalls != 2 {
		t.Fatalf("calls primary=%d backup=%d, want %d and 2", primaryCalls, backupCalls, decisionReferenceAttempts)
	}
	if result.Source != "backup" || len(result.Items) != 1 {
		t.Fatalf("result=%+v", result)
	}
}

func TestDecisionReferenceTransportErrorIsActionableAndRedacted(t *testing.T) {
	err := decisionReferenceTransportError(errors.New("proxyconnect tcp: user:secret@example: CONNECT tunnel failed, response 407"))
	if got := err.Error(); got != "proxy authentication rejected (HTTP 407)" || strings.Contains(got, "secret") {
		t.Fatalf("transport error=%q", got)
	}
}

func TestHasFreshDecisionFinancialDataset(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	now := time.Now()
	if err := store.UpsertDecisionFinancialFacts(ctx, []decisionFinancialFact{{
		Symbol: "603228", ReportPeriod: "2026-06-30", Dataset: "fina_indicator",
		ROE: 8.2, Source: "backup", FetchedAt: now,
	}}); err != nil {
		t.Fatalf("seed financial fact: %v", err)
	}
	fresh, err := store.HasFreshDecisionFinancialDataset(ctx, "603228", "fina_indicator", now.Add(-time.Hour))
	if err != nil || !fresh {
		t.Fatalf("fresh=%v err=%v, want cached dataset", fresh, err)
	}
	stale, err := store.HasFreshDecisionFinancialDataset(ctx, "603228", "fina_indicator", now.Add(time.Hour))
	if err != nil || stale {
		t.Fatalf("stale=%v err=%v, want no fresh dataset", stale, err)
	}
}

func TestDecisionDisclosureExpectedPeriod(t *testing.T) {
	if got := decisionDisclosureExpectedPeriod(decisionMarketEvent{EventDate: "2026-08-29", Title: "定期报告披露"}); got != "2026-06-30" {
		t.Fatalf("expected period=%q", got)
	}
	if got := decisionDisclosureExpectedPeriod(decisionMarketEvent{EventDate: "2026-08-29", Title: "定期报告披露（period=2026-03-31）"}); got != "2026-03-31" {
		t.Fatalf("explicit expected period=%q", got)
	}
}
