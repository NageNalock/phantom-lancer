package storage

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
)

func TestCodexGatewayAccountRoutingUsesPlanModels(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "phantom-lancer.db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	if err := store.UpsertCodexGatewayModelsForPlan(ctx, "free", []CodexGatewayModelInput{{ID: "gpt-5-codex", DisplayName: "GPT-5 Codex"}}); err != nil {
		t.Fatalf("upsert free models: %v", err)
	}
	if err := store.UpsertCodexGatewayModelsForPlan(ctx, "pro", []CodexGatewayModelInput{{ID: "gpt-5.4", DisplayName: "GPT-5.4"}}); err != nil {
		t.Fatalf("upsert pro models: %v", err)
	}

	free, err := store.CreateCodexGatewayAccount(ctx, CodexGatewayAccountInput{
		Label:       "free",
		Status:      "active",
		AccessToken: "free-access",
		Plan:        "free",
	})
	if err != nil {
		t.Fatalf("create free account: %v", err)
	}
	pro, err := store.CreateCodexGatewayAccount(ctx, CodexGatewayAccountInput{
		Label:       "pro",
		Status:      "active",
		AccessToken: "pro-access",
		Plan:        "pro",
	})
	if err != nil {
		t.Fatalf("create pro account: %v", err)
	}

	selected, err := store.SelectCodexGatewayAccountForModel(ctx, "gpt-5.4", nil)
	if err != nil {
		t.Fatalf("select pro account: %v", err)
	}
	if selected.ID != pro.ID || selected.AccessToken != "pro-access" {
		t.Fatalf("selected = %#v, want pro account %s", selected, pro.ID)
	}

	selected, err = store.SelectCodexGatewayAccountForModel(ctx, "gpt-5-codex", []string{free.ID})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("select excluded free err = %v, want ErrNotFound", err)
	}
	if selected.ID != "" {
		t.Fatalf("selected should be empty when excluded: %#v", selected)
	}
}

func TestCodexGatewayAPIKeyLifecycle(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "phantom-lancer.db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	key, err := store.CreateCodexGatewayAPIKey(ctx, "local test", "hash-1")
	if err != nil {
		t.Fatalf("create key: %v", err)
	}
	secret, err := store.GetActiveCodexGatewayAPIKeyByHash(ctx, "hash-1")
	if err != nil {
		t.Fatalf("get key by hash: %v", err)
	}
	if secret.ID != key.ID || secret.KeyHash != "hash-1" {
		t.Fatalf("secret = %#v", secret)
	}

	if _, err := store.RotateCodexGatewayAPIKey(ctx, key.ID, "hash-2"); err != nil {
		t.Fatalf("rotate key: %v", err)
	}
	if _, err := store.GetActiveCodexGatewayAPIKeyByHash(ctx, "hash-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old hash err = %v, want ErrNotFound", err)
	}
	if _, err := store.GetActiveCodexGatewayAPIKeyByHash(ctx, "hash-2"); err != nil {
		t.Fatalf("new hash should work: %v", err)
	}

	if _, err := store.UpdateCodexGatewayAPIKeyStatus(ctx, key.ID, "disabled"); err != nil {
		t.Fatalf("disable key: %v", err)
	}
	if _, err := store.GetActiveCodexGatewayAPIKeyByHash(ctx, "hash-2"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("disabled hash err = %v, want ErrNotFound", err)
	}
}

func TestCodexGatewayRequestLogCreatePrunesOverRetention(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "phantom-lancer.db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	originalRetention := codexGatewayRequestLogRetention
	codexGatewayRequestLogRetention = 3
	t.Cleanup(func() {
		codexGatewayRequestLogRetention = originalRetention
	})

	for i := 0; i < 5; i++ {
		if err := store.CreateCodexGatewayRequestLog(ctx, CodexGatewayRequestLogInput{
			RequestID:  fmt.Sprintf("req-%d", i),
			APIKind:    "chat.completions",
			Model:      "gpt-5-codex",
			StatusCode: 200,
			LatencyMS:  10 + i,
		}); err != nil {
			t.Fatalf("create request log %d: %v", i, err)
		}
	}

	logs, err := store.ListCodexGatewayRequestLogs(ctx, 10)
	if err != nil {
		t.Fatalf("list request logs: %v", err)
	}
	if len(logs) != 3 {
		t.Fatalf("request log count = %d, want 3", len(logs))
	}
}

func TestPruneCodexGatewayRequestLogsKeepsNewestRows(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "phantom-lancer.db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	for i := 0; i < 5; i++ {
		createdAt := fmt.Sprintf("2026-06-05T00:00:%02dZ", i)
		_, err := store.db.ExecContext(ctx, `
INSERT INTO codex_gateway_request_logs (
  id, request_id, api_kind, model, account_id, source_ip, status_code, error_code, error_source, error_message, latency_ms, streamed, input_tokens, output_tokens, created_at
) VALUES (?, ?, 'responses', 'gpt-5-codex', '', '', 200, '', '', '', 10, 0, 0, 0, ?)`,
			fmt.Sprintf("cgreq_%d", i), fmt.Sprintf("req-%d", i), createdAt)
		if err != nil {
			t.Fatalf("insert request log %d: %v", i, err)
		}
	}

	if err := store.PruneCodexGatewayRequestLogs(ctx, 3); err != nil {
		t.Fatalf("prune request logs: %v", err)
	}
	logs, err := store.ListCodexGatewayRequestLogs(ctx, 10)
	if err != nil {
		t.Fatalf("list request logs: %v", err)
	}
	got := []string{}
	for _, log := range logs {
		got = append(got, log.RequestID)
	}
	want := []string{"req-4", "req-3", "req-2"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("remaining request ids = %v, want %v", got, want)
	}
}
