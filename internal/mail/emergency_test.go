package mail

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"phantom-lancer/internal/events"
	"phantom-lancer/internal/mail/configapply"
	"phantom-lancer/internal/storage"
)

func newEmergencyTestService(t *testing.T) (*Service, *storage.Store, context.Context, string) {
	t.Helper()
	ctx := context.Background()
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "phantom-lancer.db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	dataDir := t.TempDir()
	svc := NewService(store, events.NewHub(), dataDir, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := svc.Ensure(ctx); err != nil {
		t.Fatalf("ensure mail service: %v", err)
	}
	configPath := svc.cli.ConfigPath
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("mkdir config: %v", err)
	}
	seed := []byte("Hostname: mx.example.test\nAdminAddress: admin@example.test\n")
	if err := os.WriteFile(configPath, seed, 0o600); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	if _, err := store.MailUpsertSetup(ctx, storage.MailSetupUpdate{
		AdminEmail:  "admin@example.test",
		Hostname:    "mx.example.test",
		WebmailAddr: "127.0.0.1:10444",
		WebAPIAddr:  "127.0.0.1:10445",
		BinaryPath:  filepath.Join(filepath.Dir(filepath.Dir(configPath)), "bin", "mox"),
		ConfigPath:  configPath,
		DataDir:     filepath.Join(filepath.Dir(filepath.Dir(configPath)), "data"),
	}); err != nil {
		t.Fatalf("upsert setup: %v", err)
	}
	// Avoid calling a real mox binary from configapply during service tests.
	svc.cli = nil
	if svc.drift != nil {
		svc.drift.SetSynced(configapply.HashBytes(seed))
	}
	return svc, store, ctx, configPath
}

func seedEmergencyDomainAndAccount(t *testing.T, store *storage.Store, ctx context.Context) {
	t.Helper()
	domain, err := store.MailCreateDomain(ctx, storage.MailDomain{
		ID:      "dom_test",
		Domain:  "example.test",
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("create domain: %v", err)
	}
	if _, err := store.MailCreateAccount(ctx, storage.MailAccount{
		ID:        "acc_test",
		DomainID:  domain.ID,
		LocalPart: "owner",
		Address:   "owner@example.test",
		Email:     "owner@example.test",
		Enabled:   true,
		Status:    "active",
	}); err != nil {
		t.Fatalf("create account: %v", err)
	}
}

func TestEmergencyEnableFailureDisablesFallbackAndAudits(t *testing.T) {
	svc, store, ctx, _ := newEmergencyTestService(t)
	seedEmergencyDomainAndAccount(t, store, ctx)

	configapply.TestStepFnOverride = map[int]func(context.Context) error{
		1: func(context.Context) error { return errors.New("forced validation failure") },
	}
	t.Cleanup(func() { configapply.TestStepFnOverride = nil })

	state, pr, err := svc.EmergencyInboundRejectEnable(ctx, EmergencyInboundRejectRequest{
		Reason:       "attack token=should-redact",
		Confirmation: "REJECT-INBOUND",
	})
	if err == nil {
		t.Fatal("expected enable failure")
	}
	if pr == nil || pr.Success {
		t.Fatalf("pipeline success=%v, want failed", pr != nil && pr.Success)
	}
	if state.Enabled {
		t.Fatal("failed enable must not leave fallback enabled")
	}
	if state.LastFailure == "" || state.LastFailureStep != 1 {
		t.Fatalf("state failure not captured: failure=%q step=%d", state.LastFailure, state.LastFailureStep)
	}
	got, err := svc.EmergencyInboundRejectGet(ctx)
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if got.Enabled {
		t.Fatal("persisted state left fallback enabled after failed enable")
	}
	audits, err := store.ListAudit(ctx, 20)
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}
	if len(audits) == 0 || audits[0].RiskLevel != "high" {
		t.Fatalf("expected high risk audit, got %#v", audits)
	}
	if payload := audits[0].Payload; strings.Contains(strings.ToLower(payload["reason_summary"].(string)), "should-redact") {
		t.Fatalf("audit reason was not redacted: %#v", payload)
	}
	if got.ActualMoxStrategy != "mox_domain_disabled" || !got.DegradedImplementation {
		t.Fatalf("fallback not explicitly marked degraded: %#v", got)
	}
}

func TestEmergencyEnableLateFailureKeepsUnknownDangerState(t *testing.T) {
	svc, store, ctx, _ := newEmergencyTestService(t)
	seedEmergencyDomainAndAccount(t, store, ctx)

	configapply.TestStepFnOverride = map[int]func(context.Context) error{
		9: func(context.Context) error { return errors.New("forced post-apply probe failure") },
	}
	t.Cleanup(func() { configapply.TestStepFnOverride = nil })

	state, pr, err := svc.EmergencyInboundRejectEnable(ctx, EmergencyInboundRejectRequest{
		Reason:       "late failure",
		Confirmation: "REJECT-INBOUND",
	})
	if err == nil {
		t.Fatal("expected enable failure")
	}
	if pr == nil || pr.Success || pr.FailureStep < 7 {
		t.Fatalf("expected late pipeline failure, got %#v", pr)
	}
	if !state.Enabled {
		t.Fatal("late apply failure with unknown rollback must keep dangerous enabled state")
	}
	if !state.ApplyUnknown || state.RestoreConflict != "apply_failed_rollback_unknown" {
		t.Fatalf("unknown rollback risk not captured: %#v", state)
	}
	persisted, err := svc.EmergencyInboundRejectGet(ctx)
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if !persisted.Enabled || !persisted.ApplyUnknown {
		t.Fatalf("persisted state lost unknown risk: %#v", persisted)
	}
}

func TestEmergencyDisableDriftPersistsConflictHashesAndAudit(t *testing.T) {
	svc, store, ctx, configPath := newEmergencyTestService(t)
	settings, err := store.MailGetSettings(ctx)
	if err != nil {
		t.Fatalf("settings: %v", err)
	}
	state := emergencyStateFromSettings(settings)
	state.Enabled = true
	state.Reason = "test"
	state.LastNormalConfigHash = "normal-hash"
	if err := svc.persistEmergencyState(ctx, settings, state); err != nil {
		t.Fatalf("persist emergency: %v", err)
	}
	if err := os.WriteFile(configPath, []byte("operator drift\n"), 0o600); err != nil {
		t.Fatalf("write drift: %v", err)
	}
	if drifted, _, err := svc.drift.Refresh(); err != nil || !drifted {
		t.Fatalf("refresh drifted=%v err=%v", drifted, err)
	}

	next, _, derr := svc.EmergencyInboundRejectDisable(ctx, EmergencyInboundRejectRequest{
		Reason:       "restore",
		Confirmation: "RESTORE-INBOUND",
	})
	if derr == nil {
		t.Fatal("expected drift conflict")
	}
	if !next.Enabled || next.RestoreConflict != "config_drifted" {
		t.Fatalf("conflict state not preserved: %#v", next)
	}
	if next.RestoreExpectedHash == "" || next.RestoreDiskHash == "" {
		t.Fatalf("conflict hashes missing: %#v", next)
	}
	persisted, err := svc.EmergencyInboundRejectGet(ctx)
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if persisted.RestoreConflict != "config_drifted" || persisted.RestoreDiskHash == "" {
		t.Fatalf("persisted conflict missing: %#v", persisted)
	}
	audits, err := store.ListAudit(ctx, 20)
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}
	if len(audits) == 0 || audits[0].Payload["restore_conflict"] != "config_drifted" {
		t.Fatalf("conflict audit missing: %#v", audits)
	}
}

func TestEmergencyAutoRestoreFailureBlocksSameDeadline(t *testing.T) {
	svc, store, ctx, configPath := newEmergencyTestService(t)
	settings, err := store.MailGetSettings(ctx)
	if err != nil {
		t.Fatalf("settings: %v", err)
	}
	deadline := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)
	state := emergencyStateFromSettings(settings)
	state.Enabled = true
	state.Reason = "test"
	state.AutoRestoreAt = deadline
	if err := svc.persistEmergencyState(ctx, settings, state); err != nil {
		t.Fatalf("persist emergency: %v", err)
	}
	if err := os.WriteFile(configPath, []byte("operator drift\n"), 0o600); err != nil {
		t.Fatalf("write drift: %v", err)
	}
	if drifted, _, err := svc.drift.Refresh(); err != nil || !drifted {
		t.Fatalf("refresh drifted=%v err=%v", drifted, err)
	}

	svc.runEmergencyAutoRestoreTick(ctx)
	eventsAfterFirst, err := store.ListEvents(ctx, EventScope, EventScope, 0, 100)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	got, err := svc.EmergencyInboundRejectGet(ctx)
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	if !got.Enabled || got.AutoRestoreBlockedAt != deadline {
		t.Fatalf("auto restore failure did not block deadline: %#v", got)
	}

	svc.runEmergencyAutoRestoreTick(ctx)
	eventsAfterSecond, err := store.ListEvents(ctx, EventScope, EventScope, 0, 100)
	if err != nil {
		t.Fatalf("list events again: %v", err)
	}
	if len(eventsAfterSecond) != len(eventsAfterFirst) {
		t.Fatalf("blocked deadline retried and wrote events: first=%d second=%d", len(eventsAfterFirst), len(eventsAfterSecond))
	}
}
