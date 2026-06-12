package codexclient

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"phantom-lancer/internal/storage"
)

// P3 scratch conversations / Memories.
//
// Scratch conversations are lightweight research/planning threads bound to a
// controlled scratch workspace. They live in the same merged conversation list
// as code threads, but keep kind=chat as a permission semantic. They are always
// read-only: no file writes, no network, no workspace-write. Memories is a
// read-only diagnostic that only reports whether Codex may use global memory /
// config / AGENTS.md; it never reads or returns the private contents of those
// files.

// ErrScratchWorkspaceUnset is returned when a chat is requested before the owner
// has chosen a scratch workspace in Codex settings.
var ErrScratchWorkspaceUnset = errors.New("scratch workspace is not configured")

// ChatThreadInput describes a new chat thread request. Only a title is accepted;
// the workspace, sandbox and approval policy are fixed by the module.
type ChatThreadInput struct {
	Title string `json:"title"`
}

// ListChats returns chat-kind threads for older clients and direct API callers.
// The primary UI uses the merged threads endpoint and treats kind=chat as a
// rendering and permission mode, not a separate navigation surface.
func (s *Service) ListChats(ctx context.Context, includeArchived bool, query string) ([]storage.CodexCliThread, error) {
	return s.store.ListCodexCliThreadsFiltered(ctx, storage.CodexCliThreadFilters{
		IncludeArchived: includeArchived,
		Query:           query,
		Kind:            "chat",
	})
}

// CreateChat creates a read-only chat thread bound to the configured scratch
// workspace. It enforces read-only sandbox and on-request approval regardless of
// the workspace defaults so a chat can never perform production changes.
func (s *Service) CreateChat(ctx context.Context, input ChatThreadInput) (storage.CodexCliThread, error) {
	scratchID := strings.TrimSpace(s.currentSettings().ScratchWorkspaceID)
	if scratchID == "" {
		return storage.CodexCliThread{}, ErrScratchWorkspaceUnset
	}
	thread, err := s.CreateThread(ctx, scratchID, strings.TrimSpace(input.Title), "", "read-only", "on-request")
	if err != nil {
		return storage.CodexCliThread{}, err
	}
	thread.Kind = "chat"
	thread.SandboxMode = "read-only"
	saved, err := s.store.SaveCodexCliThread(ctx, thread)
	if err != nil {
		return storage.CodexCliThread{}, err
	}
	_, _ = s.store.AddAudit(ctx, storage.AuditEvent{EventType: "codex_cli.chat.created", WorkspaceID: scratchID, RiskLevel: "low", Summary: "已创建只读 Codex chat", Payload: map[string]any{"threadId": saved.ID}})
	return saved, nil
}

// MemoryDiagnostics is a redacted, content-free report about whether Codex may
// use global memory / config / AGENTS.md on this host. It carries booleans and a
// path summary only; it never reads or returns file contents.
type MemoryDiagnostics struct {
	CodexHomeSummary  string `json:"codexHomeSummary"`
	ConfigPresent     bool   `json:"configPresent"`
	GlobalAgentsMD    bool   `json:"globalAgentsMd"`
	SessionsPresent   bool   `json:"sessionsPresent"`
	ScratchAgentsMD   bool   `json:"scratchAgentsMd"`
	ScratchConfigured bool   `json:"scratchConfigured"`
	Note              string `json:"note"`
}

// MemoryDiagnostics inspects the resolved Codex home and the scratch workspace
// for the mere presence of memory-bearing files. AGENTS.md guidance: do not read
// or surface private memory; only report existence so the owner understands what
// context Codex may use.
func (s *Service) MemoryDiagnostics(ctx context.Context) MemoryDiagnostics {
	report := MemoryDiagnostics{
		Note: "仅展示 Codex 是否可能使用全局 memory/config/AGENTS.md；不读取或展示其内容。项目长期规则请写入仓库 AGENTS.md。",
	}
	home := s.resolveCodexHome()
	if home != "" {
		report.CodexHomeSummary = summarizePath(home)
		report.ConfigPresent = fileExists(filepath.Join(home, "config.toml"))
		report.GlobalAgentsMD = fileExists(filepath.Join(home, "AGENTS.md"))
		report.SessionsPresent = dirHasEntries(filepath.Join(home, "sessions")) || dirHasEntries(filepath.Join(home, "history"))
	}
	settings := s.currentSettings()
	if scratchID := strings.TrimSpace(settings.ScratchWorkspaceID); scratchID != "" {
		report.ScratchConfigured = true
		if ws, err := s.store.GetCodexCliWorkspace(ctx, scratchID); err == nil && ws.Path != "" {
			report.ScratchAgentsMD = fileExists(filepath.Join(ws.Path, "AGENTS.md"))
		}
	}
	return report
}

// resolveCodexHome returns the configured CODEX_HOME, the environment value, or
// the default ~/.codex. It only computes a path; it does not open any file.
func (s *Service) resolveCodexHome() string {
	if configured := strings.TrimSpace(s.currentSettings().CodexHome); configured != "" {
		return configured
	}
	if value := strings.TrimSpace(os.Getenv("CODEX_HOME")); value != "" {
		return value
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".codex")
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func dirHasEntries(path string) bool {
	entries, err := os.ReadDir(path)
	return err == nil && len(entries) > 0
}

// summarizePath collapses an absolute path to a non-identifying summary so the
// owner home directory and username are not leaked into responses or audit.
func summarizePath(path string) string {
	base := filepath.Base(strings.TrimRight(path, string(os.PathSeparator)))
	if base == "" || base == "." || base == string(os.PathSeparator) {
		return "…"
	}
	return filepath.Join("…", base)
}
