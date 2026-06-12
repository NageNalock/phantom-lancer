package codexclient

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"phantom-lancer/internal/events"
	"phantom-lancer/internal/storage"
)

func newP3TestService(t *testing.T) (*Service, *storage.Store, string) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	store, err := storage.Open(ctx, filepath.Join(dir, "phantom-lancer.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(store, events.NewHub(), dir, func() ([]string, error) { return []string{dir}, nil }, nil)
	cleanupCodexTestService(t, svc, store)
	return svc, store, dir
}

func TestCreateChatRequiresScratchWorkspace(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newP3TestService(t)
	if _, err := svc.CreateChat(ctx, ChatThreadInput{Title: "plan"}); !errors.Is(err, ErrScratchWorkspaceUnset) {
		t.Fatalf("expected ErrScratchWorkspaceUnset, got %v", err)
	}
}

func TestCreateChatIsReadOnlyAndChatKind(t *testing.T) {
	ctx := context.Background()
	svc, _, dir := newP3TestService(t)
	wsPath := filepath.Join(dir, "scratch")
	if err := os.Mkdir(wsPath, 0o700); err != nil {
		t.Fatal(err)
	}
	// A trusted workspace would normally allow workspace-write; chats must stay
	// read-only regardless.
	ws, err := svc.CreateWorkspaceWithOptions(ctx, storage.CodexCliWorkspace{Path: wsPath, TrustState: "trusted", DefaultSandbox: "workspace-write", DefaultApprovalPolicy: "on-request"}, CreateWorkspaceOptions{CreateIfMissing: false})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateSettings(ctx, Settings{ScratchWorkspaceID: ws.ID}); err != nil {
		t.Fatal(err)
	}

	chat, err := svc.CreateChat(ctx, ChatThreadInput{Title: "plan"})
	if err != nil {
		t.Fatal(err)
	}
	if chat.Kind != "chat" {
		t.Fatalf("expected chat kind, got %q", chat.Kind)
	}
	if chat.SandboxMode != "read-only" {
		t.Fatalf("expected read-only chat, got %q", chat.SandboxMode)
	}

	// ListChats remains a compatibility endpoint for older callers and must only
	// return chat-kind threads, never code threads.
	codeThread, err := svc.CreateThread(ctx, ws.ID, "code", "", "read-only", "on-request")
	if err != nil {
		t.Fatal(err)
	}
	chats, err := svc.ListChats(ctx, false, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(chats) != 1 || chats[0].ID != chat.ID {
		t.Fatalf("expected only the chat thread, got %d items", len(chats))
	}
	if chats[0].ID == codeThread.ID {
		t.Fatal("code thread leaked into chat list")
	}

	allThreads, err := svc.ListThreadsFiltered(ctx, ThreadListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(allThreads) != 2 {
		t.Fatalf("expected merged thread list to include code and chat threads, got %d", len(allThreads))
	}

	// The optional kind filter still isolates code threads for compatibility and
	// diagnostics, even though the default UI now shows a merged conversation list.
	codeThreads, err := svc.ListThreadsFiltered(ctx, ThreadListOptions{Kind: "code"})
	if err != nil {
		t.Fatal(err)
	}
	for _, thread := range codeThreads {
		if thread.Kind == "chat" {
			t.Fatalf("chat thread %s leaked into kind=code filter", thread.ID)
		}
		if thread.ID == chat.ID {
			t.Fatalf("chat %s leaked into kind=code filter", chat.ID)
		}
	}
	if len(codeThreads) != 1 || codeThreads[0].ID != codeThread.ID {
		t.Fatalf("expected only the code thread in kind=code filter, got %d items", len(codeThreads))
	}
}

func TestChatTurnForcedReadOnlyEvenIfWorkspaceWriteRequested(t *testing.T) {
	ctx := context.Background()
	svc, store, dir := newP3TestService(t)
	wsPath := filepath.Join(dir, "scratch")
	if err := os.Mkdir(wsPath, 0o700); err != nil {
		t.Fatal(err)
	}
	ws, err := svc.CreateWorkspaceWithOptions(ctx, storage.CodexCliWorkspace{Path: wsPath, TrustState: "trusted", DefaultSandbox: "workspace-write", DefaultApprovalPolicy: "on-request"}, CreateWorkspaceOptions{CreateIfMissing: false})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateSettings(ctx, Settings{Enabled: true, ScratchWorkspaceID: ws.ID, ExecFallbackEnabled: false}); err != nil {
		t.Fatal(err)
	}
	chat, err := svc.CreateChat(ctx, ChatThreadInput{Title: "plan"})
	if err != nil {
		t.Fatal(err)
	}

	// Request workspace-write; the chat must downgrade to read-only. With exec
	// fallback disabled and no app-server the dispatch fails with ErrNoRunner,
	// but the persisted turn record still proves the enforced sandbox.
	turn, err := svc.StartTurn(ctx, chat.ID, TurnInput{Prompt: "draft a plan", Sandbox: "workspace-write", ApprovalPolicy: "on-request"})
	if err != nil && !errors.Is(err, ErrNoRunner) {
		t.Fatalf("unexpected start error: %v", err)
	}
	stored, err := store.GetCodexCliTurn(ctx, turn.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.SandboxMode != "read-only" {
		t.Fatalf("expected chat turn forced to read-only, got %q", stored.SandboxMode)
	}
}

func TestMemoryDiagnosticsReportsPresenceOnly(t *testing.T) {
	ctx := context.Background()
	svc, _, dir := newP3TestService(t)
	home := filepath.Join(dir, "codex-home")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("model = \"x\"\nsecret_token = \"abc\""), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "AGENTS.md"), []byte("rules"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateSettings(ctx, Settings{CodexHome: home}); err != nil {
		t.Fatal(err)
	}

	report := svc.MemoryDiagnostics(ctx)
	if !report.ConfigPresent || !report.GlobalAgentsMD {
		t.Fatalf("expected config and AGENTS.md detected, got %+v", report)
	}
	if report.ScratchConfigured {
		t.Fatal("expected scratch not configured")
	}
	// Path summary must not leak the full absolute home path or the temp prefix.
	if report.CodexHomeSummary == home || filepath.IsAbs(report.CodexHomeSummary) {
		t.Fatalf("expected redacted path summary, got %q", report.CodexHomeSummary)
	}
}
