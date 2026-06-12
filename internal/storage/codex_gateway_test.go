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

func TestCodexGatewayAccountPatchWithoutTokensPreservesStoredTokens(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "phantom-lancer.db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	account, err := store.CreateCodexGatewayAccount(ctx, CodexGatewayAccountInput{
		Label:        "primary",
		Status:       "active",
		AccessToken:  "old-access",
		RefreshToken: "old-refresh",
	})
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	if _, err := store.UpdateCodexGatewayAccountTokens(ctx, account.ID, "new-access", "new-refresh", "2030-01-01T00:00:00Z"); err != nil {
		t.Fatalf("refresh tokens: %v", err)
	}
	label := "renamed"
	if _, err := store.UpdateCodexGatewayAccount(ctx, account.ID, CodexGatewayAccountPatch{Label: &label}); err != nil {
		t.Fatalf("patch account: %v", err)
	}
	secret, err := store.GetCodexGatewayAccountSecret(ctx, account.ID)
	if err != nil {
		t.Fatalf("get account secret: %v", err)
	}
	if secret.AccessToken != "new-access" || secret.RefreshToken != "new-refresh" {
		t.Fatalf("tokens were overwritten: access=%q refresh=%q", secret.AccessToken, secret.RefreshToken)
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

	if err := pruneCodexGatewayRequestLogs(ctx, store.db, 3); err != nil {
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

func TestCodexGatewaySettingsHealthCheckDefaultsRoundTrip(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "phantom-lancer.db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	defaults := DefaultCodexGatewaySettings()
	if defaults.AccountHealthCheckIntervalSeconds != 43200 {
		t.Fatalf("default health interval = %d, want 43200", defaults.AccountHealthCheckIntervalSeconds)
	}

	// Fresh DB: Ensure inserts row; Get should return the default value without
	// Normalize clobbering anything.
	got, err := store.GetCodexGatewaySettings(ctx)
	if err != nil {
		t.Fatalf("get settings: %v", err)
	}
	if got.AccountHealthCheckIntervalSeconds != 43200 {
		t.Fatalf("fresh-DB health interval = %d, want 43200", got.AccountHealthCheckIntervalSeconds)
	}

	// 0 explicitly disables the loop; Normalize must NOT rewrite it to default.
	got.AccountHealthCheckIntervalSeconds = 0
	if _, err := store.UpdateCodexGatewaySettings(ctx, got); err != nil {
		t.Fatalf("update disabled: %v", err)
	}
	gotAgain, err := store.GetCodexGatewaySettings(ctx)
	if err != nil {
		t.Fatalf("re-get disabled: %v", err)
	}
	if gotAgain.AccountHealthCheckIntervalSeconds != 0 {
		t.Fatalf("disabled health interval = %d, want 0", gotAgain.AccountHealthCheckIntervalSeconds)
	}

	// Negative values must fall back to the default (per Normalize policy).
	gotAgain.AccountHealthCheckIntervalSeconds = -5
	if _, err := store.UpdateCodexGatewaySettings(ctx, gotAgain); err != nil {
		t.Fatalf("update negative: %v", err)
	}
	normNeg, err := store.GetCodexGatewaySettings(ctx)
	if err != nil {
		t.Fatalf("re-get negative: %v", err)
	}
	if normNeg.AccountHealthCheckIntervalSeconds != 43200 {
		t.Fatalf("negative-clamped health interval = %d, want 43200", normNeg.AccountHealthCheckIntervalSeconds)
	}

	// Massive positive value must be clamped to the 30-day ceiling.
	const monthSeconds = 30 * 24 * 3600
	normNeg.AccountHealthCheckIntervalSeconds = 100_000_000
	if _, err := store.UpdateCodexGatewaySettings(ctx, normNeg); err != nil {
		t.Fatalf("update huge: %v", err)
	}
	big, err := store.GetCodexGatewaySettings(ctx)
	if err != nil {
		t.Fatalf("re-get huge: %v", err)
	}
	if big.AccountHealthCheckIntervalSeconds > monthSeconds {
		t.Fatalf("upper-clamped health interval = %d, want <= %d", big.AccountHealthCheckIntervalSeconds, monthSeconds)
	}
}
