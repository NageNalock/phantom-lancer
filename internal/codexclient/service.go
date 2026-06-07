package codexclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"phantom-lancer/internal/events"
	"phantom-lancer/internal/ids"
	"phantom-lancer/internal/storage"
)

// Attachment limits for image inputs.
const (
	MaxAttachmentBytes   = 8 * 1024 * 1024
	MaxAttachmentsPerReq = 4
	attachmentTTL        = 24 * time.Hour
)

var allowedAttachmentTypes = map[string]string{
	"image/png":  ".png",
	"image/jpeg": ".jpg",
	"image/webp": ".webp",
	"image/gif":  ".gif",
}

// ErrModuleDisabled is returned when the Codex module is turned off.
var ErrModuleDisabled = errors.New("codex module disabled")

// ErrNoRunner is returned when neither app-server nor exec fallback is available.
var ErrNoRunner = errors.New("no available codex runner")

// ErrTurnInProgress is returned when another Codex turn is already active.
var ErrTurnInProgress = errors.New("codex turn already in progress")

// Service orchestrates the Codex CLI client module: detection, app-server
// runtime, workspaces, threads, turns, approvals, attachments and events.
type Service struct {
	store      *storage.Store
	hub        *events.Hub
	log        *slog.Logger
	dataDir    string
	detector   *Detector
	supervisor *AppServerSupervisor
	policy     *WorkspacePolicy
	mapper     *EventMapper
	execClient *ExecClient

	mu         sync.RWMutex
	settings   Settings
	activeTurn *appTurnContext
	// pendingApprovalReqID maps an approval record id to the live JSON-RPC
	// request id (raw, since the upstream RequestId may be a string or integer)
	// so a decision can be returned to app-server via Respond. It is only
	// populated for the in-process app-server runtime; after a restart the map
	// is empty and pending approvals fail closed.
	pendingApprovalReqID map[string]json.RawMessage
}

type appTurnContext struct {
	threadID      string
	turnID        string
	codexThreadID string
	codexTurnID   string
}

// TurnInput describes a requested turn.
type TurnInput struct {
	Prompt         string
	Sandbox        string
	ApprovalPolicy string
	Model          string
	AttachmentIDs  []string
}

// OverallStatus is the combined module status returned to the UI.
type OverallStatus struct {
	Enabled          bool                         `json:"enabled"`
	Installation     storage.CodexCliInstallation `json:"installation"`
	AppServer        AppServerStatus              `json:"appServer"`
	WorkspaceCount   int                          `json:"workspaceCount"`
	ThreadCount      int                          `json:"threadCount"`
	PendingApprovals int                          `json:"pendingApprovals"`
	LegacyTables     []string                     `json:"legacyTables,omitempty"`
}

func NewService(store *storage.Store, hub *events.Hub, dataDir string, allowedRoots func() ([]string, error), logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	svc := &Service{
		store:                store,
		hub:                  hub,
		log:                  logger,
		dataDir:              filepath.Join(dataDir, "codex"),
		policy:               NewWorkspacePolicy(allowedRoots),
		execClient:           NewExecClient(),
		settings:             DefaultSettings(),
		pendingApprovalReqID: make(map[string]json.RawMessage),
	}
	svc.detector = NewDetector(svc.binaryPath, svc.codexHome)
	svc.mapper = NewEventMapper(2000)
	svc.supervisor = NewAppServerSupervisor(svc.detector, svc.currentSettings, svc.handleNotification, svc.handleServerRequest, logger)
	return svc
}

func (s *Service) binaryPath() string { return s.currentSettings().BinaryPath }
func (s *Service) codexHome() string  { return s.currentSettings().CodexHome }
func (s *Service) currentSettings() Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.settings
}

// Ensure loads persisted settings and prepares runtime state. It marks any
// running turns as interrupted by the restart and clears orphan runs.
func (s *Service) Ensure(ctx context.Context) error {
	loaded, err := loadSettings(ctx, s.store)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.settings = loaded
	s.mapper = NewEventMapper(2000)
	s.mu.Unlock()

	if err := s.store.MarkCodexCliRunningThreadsInterrupted(ctx, "interrupted_by_server_restart"); err != nil {
		return err
	}
	if affected, err := s.store.MarkCodexCliOrphanRuns(ctx, "interrupted_by_server_restart"); err != nil {
		return err
	} else if affected > 0 {
		s.log.Warn("codex cli marked orphan runs", "count", affected)
	}
	if err := os.MkdirAll(filepath.Join(s.dataDir, "attachments"), 0o700); err != nil {
		return err
	}
	// Best-effort initial detection on startup.
	go func() {
		detectCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = s.Probe(detectCtx)
	}()
	return nil
}

// StartBackground launches the periodic app-server probe loop, attachment GC and
// the daily event-retention cleanup.
func (s *Service) StartBackground(ctx context.Context) {
	s.supervisor.StartProbeLoop(ctx)
	go func() {
		ticker := time.NewTicker(15 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.cleanupAttachments(ctx)
			}
		}
	}()
	go func() {
		// Run once shortly after start, then daily.
		timer := time.NewTimer(2 * time.Minute)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				s.cleanupExpiredEvents(ctx)
				timer.Reset(24 * time.Hour)
			}
		}
	}()
	if s.currentSettings().AppServerStartOnLaunch && s.currentSettings().AppServerEnabled {
		go func() {
			startCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if _, err := s.StartAppServer(startCtx); err != nil {
				s.log.Warn("codex app-server auto-start failed", "summary", Redact(err.Error(), 160))
			}
		}()
	}
}

// Close stops the managed app-server.
func (s *Service) Close() {
	s.supervisor.Stop(context.Background())
}

// ---- settings ----

func (s *Service) Settings(ctx context.Context) Settings { return s.currentSettings() }

func (s *Service) UpdateSettings(ctx context.Context, next Settings) (Settings, error) {
	next = normalizeSettings(next)
	if err := saveSettings(ctx, s.store, next); err != nil {
		return Settings{}, err
	}
	s.mu.Lock()
	s.settings = next
	s.mu.Unlock()
	return next, nil
}

// ---- detection / status ----

func (s *Service) Probe(ctx context.Context) error {
	result := s.detector.Detect(ctx)
	caps := map[string]any{
		"binaryFound":  result.Capabilities.BinaryFound,
		"version":      result.Capabilities.Version,
		"appServer":    result.Capabilities.AppServer,
		"exec":         result.Capabilities.Exec,
		"doctor":       result.Capabilities.Doctor,
		"authState":    result.Capabilities.AuthState,
		"sandboxState": result.Capabilities.SandboxState,
	}
	_, err := s.store.UpsertCodexCliInstallation(ctx, storage.CodexCliInstallation{
		BinaryPath:     result.BinaryPath,
		Version:        result.Version,
		Status:         result.Status,
		Capabilities:   caps,
		DoctorSummary:  result.DoctorSummary,
		LastProbeError: result.LastProbeError,
	})
	if err != nil {
		return err
	}
	if result.LastProbeError != "" {
		s.log.Warn("codex cli probe error", "summary", result.LastProbeError)
		_, _ = s.store.AddAudit(ctx, storage.AuditEvent{EventType: "codex_cli.probe.failed", RiskLevel: "low", Summary: "Codex CLI 探测失败", Payload: map[string]any{"error": result.LastProbeError}})
	}
	return nil
}

func (s *Service) Status(ctx context.Context) (OverallStatus, error) {
	install, err := s.store.GetCodexCliInstallation(ctx)
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		return OverallStatus{}, err
	}
	workspaces, _ := s.store.ListCodexCliWorkspaces(ctx)
	threads, _ := s.store.ListCodexCliThreads(ctx, false, "")
	approvals, _ := s.store.ListCodexCliApprovals(ctx, "pending", "")
	legacy, _ := s.store.CodexCliLegacyTablesDetected(ctx)
	if len(legacy) > 0 {
		_, _ = s.store.AddAudit(ctx, storage.AuditEvent{EventType: "codex_cli.legacy_data.detected", RiskLevel: "low", Summary: "检测到旧版 Codex 数据残留", Payload: map[string]any{"tables": legacy}})
	}
	return OverallStatus{
		Enabled:          s.currentSettings().Enabled,
		Installation:     install,
		AppServer:        s.supervisor.Status(),
		WorkspaceCount:   len(workspaces),
		ThreadCount:      len(threads),
		PendingApprovals: len(approvals),
		LegacyTables:     legacy,
	}, nil
}

// ---- app-server control ----

func (s *Service) AppServerStatus() AppServerStatus { return s.supervisor.Status() }

func (s *Service) StartAppServer(ctx context.Context) (AppServerStatus, error) {
	if !s.currentSettings().Enabled {
		return AppServerStatus{}, ErrModuleDisabled
	}
	_, _ = s.store.AddAudit(ctx, storage.AuditEvent{EventType: "codex_cli.app_server.start_requested", RiskLevel: "medium", Summary: "请求启动 Codex app-server"})
	status, err := s.supervisor.Start(ctx)
	if err != nil {
		_, _ = s.store.AddAudit(ctx, storage.AuditEvent{EventType: "codex_cli.app_server.start_failed", RiskLevel: "medium", Summary: "Codex app-server 启动失败", Payload: map[string]any{"error": Redact(err.Error(), 160)}})
		return status, err
	}
	if status.State == RuntimeRunning {
		_, _ = s.store.AddAudit(ctx, storage.AuditEvent{EventType: "codex_cli.app_server.started", RiskLevel: "low", Summary: "Codex app-server 已启动"})
	}
	return status, nil
}

func (s *Service) StopAppServer(ctx context.Context) AppServerStatus {
	status := s.supervisor.Stop(ctx)
	_, _ = s.store.AddAudit(ctx, storage.AuditEvent{EventType: "codex_cli.app_server.stopped", RiskLevel: "low", Summary: "Codex app-server 已停止"})
	return status
}

func (s *Service) RestartAppServer(ctx context.Context) (AppServerStatus, error) {
	return s.supervisor.Restart(ctx)
}

// ListModels probes the live model catalog via the app-server model/list method.
// It returns an empty list when the app-server is not running, so callers can
// fall back to the configured default model.
func (s *Service) ListModels(ctx context.Context) ([]CodexModel, error) {
	client := s.supervisor.Client()
	if client == nil {
		return []CodexModel{}, nil
	}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	raw, err := client.Call(cctx, "model/list", map[string]any{"limit": 50})
	if err != nil {
		return []CodexModel{}, nil
	}
	return parseModelList(raw), nil
}

// ---- workspaces ----

func (s *Service) ListWorkspaces(ctx context.Context) ([]storage.CodexCliWorkspace, error) {
	return s.store.ListCodexCliWorkspaces(ctx)
}

func (s *Service) CreateWorkspace(ctx context.Context, ws storage.CodexCliWorkspace) (storage.CodexCliWorkspace, error) {
	normalized, err := s.policy.NormalizeWorkspacePath(ws.Path)
	if err != nil {
		return storage.CodexCliWorkspace{}, err
	}
	ws.Path = normalized
	created, err := s.store.CreateCodexCliWorkspace(ctx, ws)
	if err != nil {
		return storage.CodexCliWorkspace{}, err
	}
	_, _ = s.store.AddAudit(ctx, storage.AuditEvent{EventType: "codex_cli.workspace.created", WorkspaceID: created.ID, RiskLevel: "low", Summary: "已登记 Codex 工作区", Payload: map[string]any{"trustState": created.TrustState}})
	return created, nil
}

func (s *Service) UpdateWorkspace(ctx context.Context, ws storage.CodexCliWorkspace) (storage.CodexCliWorkspace, error) {
	updated, err := s.store.UpdateCodexCliWorkspace(ctx, ws)
	if err != nil {
		return storage.CodexCliWorkspace{}, err
	}
	_, _ = s.store.AddAudit(ctx, storage.AuditEvent{EventType: "codex_cli.workspace.updated", WorkspaceID: updated.ID, RiskLevel: "low", Summary: "已更新 Codex 工作区", Payload: map[string]any{"trustState": updated.TrustState}})
	return updated, nil
}

func (s *Service) DeleteWorkspace(ctx context.Context, id string) error {
	return s.store.DeleteCodexCliWorkspace(ctx, id)
}

// ---- threads ----

func (s *Service) ListThreads(ctx context.Context, includeArchived bool, q string) ([]storage.CodexCliThread, error) {
	return s.store.ListCodexCliThreads(ctx, includeArchived, q)
}

func (s *Service) CreateThread(ctx context.Context, workspaceID, title, model, sandbox, approval string) (storage.CodexCliThread, error) {
	ws, err := s.store.GetCodexCliWorkspace(ctx, workspaceID)
	if err != nil {
		return storage.CodexCliThread{}, err
	}
	if strings.TrimSpace(sandbox) == "" {
		sandbox = ws.DefaultSandbox
	}
	if strings.TrimSpace(approval) == "" {
		approval = ws.DefaultApprovalPolicy
	}
	if strings.TrimSpace(model) == "" {
		model = ws.DefaultModel
		if strings.TrimSpace(model) == "" {
			model = s.currentSettings().DefaultModel
		}
	}
	sourceMode := "app_server"
	if s.supervisor.Client() == nil {
		sourceMode = "exec"
	}
	thread, err := s.store.CreateCodexCliThread(ctx, storage.CodexCliThread{
		WorkspaceID:    workspaceID,
		Title:          title,
		Model:          model,
		SandboxMode:    sandbox,
		ApprovalPolicy: approval,
		SourceMode:     sourceMode,
	})
	if err != nil {
		return storage.CodexCliThread{}, err
	}
	_ = s.store.TouchCodexCliWorkspace(ctx, workspaceID)
	_, _ = s.store.AddAudit(ctx, storage.AuditEvent{EventType: "codex_cli.thread.created", WorkspaceID: workspaceID, RiskLevel: "low", Summary: "已创建 Codex 会话", Payload: map[string]any{"threadId": thread.ID, "sandbox": sandbox, "approval": approval}})
	return thread, nil
}

func (s *Service) GetThread(ctx context.Context, id string) (storage.CodexCliThread, error) {
	return s.store.GetCodexCliThread(ctx, id)
}

func (s *Service) ListTurns(ctx context.Context, threadID string) ([]storage.CodexCliTurn, error) {
	return s.store.ListCodexCliTurns(ctx, threadID)
}

func (s *Service) PatchThread(ctx context.Context, id string, title *string, pinned *bool) (storage.CodexCliThread, error) {
	thread, err := s.store.GetCodexCliThread(ctx, id)
	if err != nil {
		return storage.CodexCliThread{}, err
	}
	if title != nil {
		thread.Title = strings.TrimSpace(*title)
	}
	if pinned != nil {
		thread.Pinned = *pinned
	}
	return s.store.SaveCodexCliThread(ctx, thread)
}

func (s *Service) ArchiveThread(ctx context.Context, id string) (storage.CodexCliThread, error) {
	thread, err := s.store.GetCodexCliThread(ctx, id)
	if err != nil {
		return storage.CodexCliThread{}, err
	}
	if client := s.supervisor.Client(); client != nil && thread.SourceMode == "app_server" && thread.CodexThreadID != "" {
		cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		_, err = client.Call(cctx, "thread/archive", map[string]any{"threadId": thread.CodexThreadID})
		cancel()
		if err != nil {
			return storage.CodexCliThread{}, fmt.Errorf("upstream archive failed: %w", err)
		}
	}
	thread.ArchivedAt = time.Now().UTC().Format(time.RFC3339Nano)
	thread.Status = "archived"
	saved, err := s.store.SaveCodexCliThread(ctx, thread)
	if err != nil {
		return storage.CodexCliThread{}, err
	}
	s.appendThreadEvent(ctx, saved.ID, "", EventThreadArchived, "codex", "", "", nil)
	_, _ = s.store.AddAudit(ctx, storage.AuditEvent{EventType: "codex_cli.thread.archived", RiskLevel: "low", Summary: "已归档 Codex 会话", Payload: map[string]any{"threadId": saved.ID}})
	return saved, nil
}

func (s *Service) ResumeThread(ctx context.Context, id string) (storage.CodexCliThread, error) {
	thread, err := s.store.GetCodexCliThread(ctx, id)
	if err != nil {
		return storage.CodexCliThread{}, err
	}
	if thread.ArchivedAt != "" {
		thread.ArchivedAt = ""
	}
	if thread.Status == "archived" {
		thread.Status = "idle"
	}
	// Best-effort deep resume: when the app-server is live and this thread has a
	// prior codex thread id, ask app-server to reload the conversation context so
	// the next turn continues the same session. Failures degrade to a fresh
	// thread/start on the next turn.
	if client := s.supervisor.Client(); client != nil && thread.SourceMode == "app_server" && thread.CodexThreadID != "" {
		cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		if _, rerr := client.Call(cctx, "thread/resume", map[string]any{"threadId": thread.CodexThreadID}); rerr != nil {
			s.log.Warn("codex cli thread resume failed", "summary", Redact(rerr.Error(), 120))
			// Clear the stale codex id so the next turn starts a new codex thread.
			thread.CodexThreadID = ""
		}
		cancel()
	}
	saved, err := s.store.SaveCodexCliThread(ctx, thread)
	if err != nil {
		return storage.CodexCliThread{}, err
	}
	s.appendThreadEvent(ctx, saved.ID, "", EventThreadResumed, "codex", "", "", nil)
	return saved, nil
}

// ForkThread creates a new thread in the same workspace, copying the source
// thread's run policy as a starting point. The forked thread starts with a fresh
// codex session (no codex thread id); upstream app-server thread forking, when
// available, can later be wired into the next turn. Returns the new thread.
func (s *Service) ForkThread(ctx context.Context, id string) (storage.CodexCliThread, error) {
	src, err := s.store.GetCodexCliThread(ctx, id)
	if err != nil {
		return storage.CodexCliThread{}, err
	}
	title := strings.TrimSpace(src.Title)
	if title == "" {
		title = "新对话"
	}
	codexThreadID := ""
	if client := s.supervisor.Client(); client != nil && src.SourceMode == "app_server" && src.CodexThreadID != "" {
		cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		raw, callErr := client.Call(cctx, "thread/fork", map[string]any{"threadId": src.CodexThreadID})
		cancel()
		if callErr != nil {
			return storage.CodexCliThread{}, fmt.Errorf("upstream fork failed: %w", callErr)
		}
		codexThreadID = extractThreadID(raw)
	}
	forked, err := s.CreateThread(ctx, src.WorkspaceID, title+"（副本）", src.Model, src.SandboxMode, src.ApprovalPolicy)
	if err != nil {
		return storage.CodexCliThread{}, err
	}
	if codexThreadID != "" {
		forked.CodexThreadID = codexThreadID
		forked.SourceMode = "app_server"
		if saved, saveErr := s.store.SaveCodexCliThread(ctx, forked); saveErr == nil {
			forked = saved
		}
	}
	return forked, nil
}

// ---- turns ----

func (s *Service) StartTurn(ctx context.Context, threadID string, input TurnInput) (storage.CodexCliTurn, error) {
	if !s.currentSettings().Enabled {
		return storage.CodexCliTurn{}, ErrModuleDisabled
	}
	if running, err := s.store.HasRunningCodexCliTurn(ctx); err != nil {
		return storage.CodexCliTurn{}, err
	} else if running {
		return storage.CodexCliTurn{}, ErrTurnInProgress
	}
	thread, err := s.store.GetCodexCliThread(ctx, threadID)
	if err != nil {
		return storage.CodexCliTurn{}, err
	}
	ws, err := s.store.GetCodexCliWorkspace(ctx, thread.WorkspaceID)
	if err != nil {
		return storage.CodexCliTurn{}, err
	}
	policy, err := s.policy.ResolveRunPolicy(ws, firstNonEmpty(input.Sandbox, thread.SandboxMode), firstNonEmpty(input.ApprovalPolicy, thread.ApprovalPolicy))
	if err != nil {
		return storage.CodexCliTurn{}, err
	}
	model := firstNonEmpty(input.Model, thread.Model, s.currentSettings().DefaultModel)

	turn, err := s.store.CreateCodexCliTurn(ctx, storage.CodexCliTurn{
		ThreadID:       threadID,
		Status:         "running",
		PromptSummary:  Preview(input.Prompt, 280),
		Model:          model,
		SandboxMode:    policy.Sandbox,
		ApprovalPolicy: policy.ApprovalPolicy,
	})
	if err != nil {
		return storage.CodexCliTurn{}, err
	}
	thread.Status = "running"
	thread.LastTurnID = turn.ID
	thread.LastError = ""
	_, _ = s.store.SaveCodexCliThread(ctx, thread)
	_ = s.store.TouchCodexCliWorkspace(ctx, thread.WorkspaceID)

	s.appendThreadEvent(ctx, threadID, turn.ID, EventTurnStarted, "codex", "", "", map[string]any{"model": model, "sandbox": policy.Sandbox, "approval": policy.ApprovalPolicy})
	s.appendThreadEvent(ctx, threadID, turn.ID, EventMessageUser, "codex", "", Preview(input.Prompt, s.currentSettings().MaxEventPayloadBytes/16), nil)
	_, _ = s.store.AddAudit(ctx, storage.AuditEvent{EventType: "codex_cli.turn.started", WorkspaceID: thread.WorkspaceID, RiskLevel: turnRisk(policy), Summary: "已开始 Codex turn", Payload: map[string]any{"threadId": threadID, "turnId": turn.ID, "sandbox": policy.Sandbox, "approval": policy.ApprovalPolicy}})

	images, err := s.attachmentPaths(ctx, threadID, input.AttachmentIDs)
	if err != nil {
		return storage.CodexCliTurn{}, err
	}

	client := s.supervisor.Client()
	if client != nil && thread.SourceMode == "app_server" {
		go s.runAppServerTurn(context.WithoutCancel(ctx), client, thread, ws, turn, input.Prompt, model, policy, images)
		return turn, nil
	}
	if !s.currentSettings().ExecFallbackEnabled {
		s.failTurn(ctx, thread, turn, "app-server unavailable and exec fallback disabled")
		return turn, ErrNoRunner
	}
	if policy.Sandbox != "read-only" && policy.ApprovalPolicy == "on-request" {
		// exec non-interactive cannot service approvals; require read-only unless
		// owner explicitly chose a non-approval policy.
		s.failTurn(ctx, thread, turn, "exec fallback requires read-only sandbox when approvals cannot be served interactively")
		return turn, errors.New("exec fallback requires read-only sandbox")
	}
	go s.runExecTurn(context.WithoutCancel(ctx), thread, ws, turn, input.Prompt, model, policy, images)
	return turn, nil
}

func (s *Service) runExecTurn(ctx context.Context, thread storage.CodexCliThread, ws storage.CodexCliWorkspace, turn storage.CodexCliTurn, prompt, model string, policy RunPolicy, images []string) {
	binary := s.detector.ResolveBinary()
	if binary == "" {
		s.failTurn(ctx, thread, turn, "codex binary not found")
		return
	}
	run, _ := s.store.CreateCodexCliRun(ctx, storage.CodexCliRun{ThreadID: thread.ID, TurnID: turn.ID, Mode: "exec", Status: "running"})
	opts := ExecOptions{
		Binary:    binary,
		CodexHome: s.currentSettings().CodexHome,
		Cwd:       ws.Path,
		Sandbox:   policy.Sandbox,
		Approval:  policy.ApprovalPolicy,
		Model:     model,
		Prompt:    prompt,
		Images:    images,
	}
	runCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	err := s.execClient.Run(runCtx, opts, func(line []byte) {
		mapped, ok := s.mapper.MapExecLine(line)
		if !ok {
			return
		}
		s.appendThreadEvent(ctx, thread.ID, turn.ID, mapped.EventType, mapped.CodexMethod, mapped.ItemType, mapped.TextPreview, mapped.Payload)
	})
	if err != nil {
		_ = s.store.FinishCodexCliRun(ctx, run.ID, "failed", 1, Redact(err.Error(), 200))
		s.failTurn(ctx, thread, turn, Redact("exec failed: "+err.Error(), 200))
		return
	}
	_ = s.store.FinishCodexCliRun(ctx, run.ID, "exited", 0, "")
	s.completeTurn(ctx, thread, turn, nil)
}

func (s *Service) runAppServerTurn(ctx context.Context, client *AppServerClient, thread storage.CodexCliThread, ws storage.CodexCliWorkspace, turn storage.CodexCliTurn, prompt, model string, policy RunPolicy, images []string) {
	run, _ := s.store.CreateCodexCliRun(ctx, storage.CodexCliRun{ThreadID: thread.ID, TurnID: turn.ID, Mode: "app_server", PID: client.PID(), Status: "running"})
	codexThreadID := thread.CodexThreadID
	if codexThreadID == "" {
		startCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		raw, err := client.Call(startCtx, "thread/start", map[string]any{
			"cwd":            ws.Path,
			"approvalPolicy": appServerApprovalPolicy(policy.ApprovalPolicy),
			"sandbox":        appServerSandboxMode(policy.Sandbox),
			"model":          model,
		})
		cancel()
		if err != nil {
			_ = s.store.FinishCodexCliRun(ctx, run.ID, "failed", 1, Redact(err.Error(), 200))
			s.failTurn(ctx, thread, turn, Redact("thread/start failed: "+err.Error(), 200))
			return
		}
		codexThreadID = extractThreadID(raw)
		thread.CodexThreadID = codexThreadID
		_, _ = s.store.SaveCodexCliThread(ctx, thread)
	}

	s.mu.Lock()
	s.activeTurn = &appTurnContext{threadID: thread.ID, turnID: turn.ID, codexThreadID: codexThreadID}
	s.mu.Unlock()

	turnCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	params := map[string]any{
		"threadId":       codexThreadID,
		"input":          buildTurnInput(prompt, images),
		"approvalPolicy": appServerApprovalPolicy(policy.ApprovalPolicy),
		"sandboxPolicy":  appServerSandboxPolicy(policy, ws.Path),
		"model":          model,
	}
	raw, err := client.Call(turnCtx, "turn/start", params)
	if err != nil {
		_ = s.store.FinishCodexCliRun(ctx, run.ID, "failed", 1, Redact(err.Error(), 200))
		s.failTurn(ctx, thread, turn, Redact("turn/start failed: "+err.Error(), 200))
		s.clearActiveTurn(turn.ID)
		return
	}
	if codexTurnID := extractTurnID(raw); codexTurnID != "" {
		turn.CodexTurnID = codexTurnID
		if saved, err := s.store.SaveCodexCliTurn(ctx, turn); err == nil {
			turn = saved
		}
		s.mu.Lock()
		if s.activeTurn != nil && s.activeTurn.turnID == turn.ID {
			s.activeTurn.codexTurnID = codexTurnID
		}
		s.mu.Unlock()
	}
	_ = s.store.FinishCodexCliRun(ctx, run.ID, "exited", 0, "")
	// Terminal turn state arrives via turn/completed notification; the goroutine
	// stays only to own the run record. Active turn is cleared by the notification
	// handler.
}

func (s *Service) InterruptTurn(ctx context.Context, turnID string) (storage.CodexCliTurn, error) {
	turn, err := s.store.GetCodexCliTurn(ctx, turnID)
	if err != nil {
		return storage.CodexCliTurn{}, err
	}
	thread, err := s.store.GetCodexCliThread(ctx, turn.ThreadID)
	if err != nil {
		return storage.CodexCliTurn{}, err
	}
	if client := s.supervisor.Client(); client != nil && thread.CodexThreadID != "" {
		cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		params := map[string]any{"threadId": thread.CodexThreadID}
		if turn.CodexTurnID != "" {
			params["turnId"] = turn.CodexTurnID
		}
		_, _ = client.Call(cctx, "turn/interrupt", params)
		cancel()
	}
	turn.Status = "cancelled"
	turn.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
	saved, _ := s.store.SaveCodexCliTurn(ctx, turn)
	thread.Status = "idle"
	_, _ = s.store.SaveCodexCliThread(ctx, thread)
	s.appendThreadEvent(ctx, thread.ID, turnID, EventTurnCancelled, "codex", "", "", nil)
	_, _ = s.store.AddAudit(ctx, storage.AuditEvent{EventType: "codex_cli.turn.interrupted", RiskLevel: "low", Summary: "已中断 Codex turn", Payload: map[string]any{"threadId": thread.ID, "turnId": turnID}})
	s.clearActiveTurn(turnID)
	return saved, nil
}

func (s *Service) SteerTurn(ctx context.Context, turnID, prompt string) error {
	turn, err := s.store.GetCodexCliTurn(ctx, turnID)
	if err != nil {
		return err
	}
	thread, err := s.store.GetCodexCliThread(ctx, turn.ThreadID)
	if err != nil {
		return err
	}
	client := s.supervisor.Client()
	if client == nil || thread.CodexThreadID == "" {
		return ErrNoRunner
	}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	steerParams := map[string]any{"threadId": thread.CodexThreadID, "input": buildTurnInput(prompt, nil)}
	if turn.CodexTurnID != "" {
		steerParams["expectedTurnId"] = turn.CodexTurnID
	}
	_, err = client.Call(cctx, "turn/steer", steerParams)
	if err != nil {
		return err
	}
	s.appendThreadEvent(ctx, thread.ID, turnID, EventMessageUser, "codex", "", Preview(prompt, 1000), nil)
	return nil
}

// ---- events ----

func (s *Service) ListThreadEvents(ctx context.Context, threadID string, after int64, limit int) ([]storage.CodexCliEvent, error) {
	return s.store.ListCodexCliEvents(ctx, threadID, after, limit)
}

func (s *Service) appendThreadEvent(ctx context.Context, threadID, turnID, eventType, method, itemType, preview string, payload map[string]any) {
	if payload == nil {
		payload = map[string]any{}
	}
	payload = limitEventPayload(payload, s.currentSettings().MaxEventPayloadBytes)
	stored, err := s.store.AppendCodexCliEvent(ctx, storage.CodexCliEvent{
		ThreadID:    threadID,
		TurnID:      turnID,
		EventType:   eventType,
		CodexMethod: method,
		ItemType:    itemType,
		Payload:     payload,
		TextPreview: preview,
	})
	if err != nil {
		s.log.Warn("codex cli append event failed", "summary", Redact(err.Error(), 120))
		return
	}
	s.hub.Publish(events.Event{
		ID:        stored.ID,
		Scope:     "codex.thread",
		ScopeID:   threadID,
		Sequence:  stored.Sequence,
		Type:      eventType,
		Payload:   map[string]any{"turnId": turnID, "itemType": itemType, "textPreview": preview, "codexMethod": method},
		CreatedAt: stored.CreatedAt,
	})
	_ = s.store.PruneCodexCliEvents(ctx, threadID, s.currentSettings().MaxEventsPerThread)
}

// handleNotification is invoked by the supervisor for every app-server
// notification (no id). Approval requests are not notifications; they arrive via
// handleServerRequest.
func (s *Service) handleNotification(notif Notification) {
	mapped, ok := s.mapper.MapAppServerNotification(notif.Method, notif.Params)
	if !ok {
		return
	}
	s.mu.RLock()
	active := s.activeTurn
	s.mu.RUnlock()
	if active == nil {
		return
	}
	ctx := context.Background()
	s.appendThreadEvent(ctx, active.threadID, active.turnID, mapped.EventType, mapped.CodexMethod, mapped.ItemType, mapped.TextPreview, mapped.Payload)

	if mapped.TurnStatus != "" {
		thread, err := s.store.GetCodexCliThread(ctx, active.threadID)
		if err != nil {
			return
		}
		turn, err := s.store.GetCodexCliTurn(ctx, active.turnID)
		if err != nil {
			return
		}
		switch mapped.TurnStatus {
		case "completed":
			s.completeTurn(ctx, thread, turn, nil)
		case "failed":
			s.failTurn(ctx, thread, turn, Preview(mapped.TextPreview, 200))
		case "cancelled":
			turn.Status = "cancelled"
			turn.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
			_, _ = s.store.SaveCodexCliTurn(ctx, turn)
			thread.Status = "idle"
			_, _ = s.store.SaveCodexCliThread(ctx, thread)
		}
		s.clearActiveTurn(active.turnID)
	}
}

// handleServerRequest is invoked by the supervisor for server-initiated requests
// that require a client response. Approval requests are persisted as resumable
// records; the JSON-RPC request id is held in memory so the decision can be
// returned to app-server via Respond.
func (s *Service) handleServerRequest(req ServerRequest) {
	approvalReq, ok := s.mapper.ParseApprovalRequest(req)
	if !ok {
		return
	}
	s.mu.RLock()
	active := s.activeTurn
	s.mu.RUnlock()
	if active == nil {
		// No active turn to attach to; deny defensively so app-server is not left
		// waiting forever.
		if client := s.supervisor.Client(); client != nil {
			_ = client.Respond(req.ID, map[string]any{"decision": "decline"})
		}
		return
	}
	ctx := context.Background()
	s.appendThreadEvent(ctx, active.threadID, active.turnID, EventApprovalReq, req.Method, approvalReq.ActionKind, approvalReq.CommandPreview, nil)
	s.recordApproval(ctx, active, &approvalReq, req.ID)
}

func (s *Service) recordApproval(ctx context.Context, active *appTurnContext, req *ApprovalRequest, jsonRPCID json.RawMessage) {
	approval, err := s.store.CreateCodexCliApproval(ctx, storage.CodexCliApproval{
		ThreadID:       active.threadID,
		TurnID:         active.turnID,
		CodexRequestID: req.CodexRequestID,
		ActionKind:     req.ActionKind,
		CommandPreview: req.CommandPreview,
		CwdSummary:     req.CwdSummary,
		RiskLevel:      req.RiskLevel,
		ExpiresAt:      time.Now().UTC().Add(10 * time.Minute).Format(time.RFC3339Nano),
	})
	if err != nil {
		s.log.Warn("codex cli record approval failed", "summary", Redact(err.Error(), 120))
		return
	}
	s.mu.Lock()
	s.pendingApprovalReqID[approval.ID] = jsonRPCID
	s.mu.Unlock()
	if turn, err := s.store.GetCodexCliTurn(ctx, active.turnID); err == nil {
		turn.Status = "waiting_approval"
		_, _ = s.store.SaveCodexCliTurn(ctx, turn)
	}
	if thread, err := s.store.GetCodexCliThread(ctx, active.threadID); err == nil {
		thread.Status = "needs_approval"
		_, _ = s.store.SaveCodexCliThread(ctx, thread)
	}
	_, _ = s.store.AddAudit(ctx, storage.AuditEvent{EventType: "codex_cli.approval.requested", RiskLevel: req.RiskLevel, Summary: "Codex 请求审批", Payload: map[string]any{"approvalId": approval.ID, "threadId": active.threadID, "actionKind": req.ActionKind, "riskLevel": req.RiskLevel}})
}

// ---- approvals ----

func (s *Service) ListApprovals(ctx context.Context, status, threadID string) ([]storage.CodexCliApproval, error) {
	return s.store.ListCodexCliApprovals(ctx, status, threadID)
}

func (s *Service) ResolveApproval(ctx context.Context, id string, approve bool) (storage.CodexCliApproval, error) {
	approval, err := s.store.GetCodexCliApproval(ctx, id)
	if err != nil {
		return storage.CodexCliApproval{}, err
	}
	if approval.Status != "pending" {
		return approval, nil
	}
	// Decision strings follow the upstream v2 approval protocol (accept/decline).
	decision := "decline"
	status := "denied"
	if approve {
		decision = "accept"
		status = "approved"
	}
	s.mu.Lock()
	jsonRPCID, hasReqID := s.pendingApprovalReqID[id]
	if hasReqID {
		delete(s.pendingApprovalReqID, id)
	}
	s.mu.Unlock()
	if client := s.supervisor.Client(); client != nil && hasReqID {
		if err := client.Respond(jsonRPCID, map[string]any{"decision": decision}); err != nil {
			s.log.Warn("codex cli approval respond failed", "summary", Redact(err.Error(), 120))
		}
	}
	resolved, err := s.store.ResolveCodexCliApproval(ctx, id, status, decision)
	if err != nil {
		return storage.CodexCliApproval{}, err
	}
	s.appendThreadEvent(ctx, approval.ThreadID, approval.TurnID, EventApprovalResolve, "codex", "", "", map[string]any{"approvalId": id, "decision": decision})
	eventType := "codex_cli.approval.denied"
	summary := "已拒绝 Codex 审批"
	if approve {
		eventType = "codex_cli.approval.approved"
		summary = "已允许 Codex 审批"
	}
	_, _ = s.store.AddAudit(ctx, storage.AuditEvent{EventType: eventType, RiskLevel: approval.RiskLevel, Summary: summary, Payload: map[string]any{"approvalId": id, "threadId": approval.ThreadID}})
	if thread, err := s.store.GetCodexCliThread(ctx, approval.ThreadID); err == nil && thread.Status == "needs_approval" {
		thread.Status = "running"
		_, _ = s.store.SaveCodexCliThread(ctx, thread)
	}
	return resolved, nil
}

// ---- attachments ----

func (s *Service) CreateAttachment(ctx context.Context, threadID, filename, contentType string, data []byte) (storage.CodexCliAttachment, error) {
	if strings.TrimSpace(threadID) == "" {
		return storage.CodexCliAttachment{}, errors.New("thread id is required")
	}
	if _, err := s.store.GetCodexCliThread(ctx, threadID); err != nil {
		return storage.CodexCliAttachment{}, err
	}
	count, err := s.store.CountActiveCodexCliAttachments(ctx, threadID, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return storage.CodexCliAttachment{}, err
	}
	if count >= MaxAttachmentsPerReq {
		return storage.CodexCliAttachment{}, fmt.Errorf("too many attachments: max %d", MaxAttachmentsPerReq)
	}
	ext, ok := allowedAttachmentTypes[strings.ToLower(strings.TrimSpace(contentType))]
	if !ok {
		return storage.CodexCliAttachment{}, fmt.Errorf("unsupported attachment type: %s", contentType)
	}
	if len(data) == 0 || len(data) > MaxAttachmentBytes {
		return storage.CodexCliAttachment{}, errors.New("attachment is empty or too large")
	}
	id, err := storageNewAttachmentID()
	if err != nil {
		return storage.CodexCliAttachment{}, err
	}
	dir := filepath.Join(s.dataDir, "attachments")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return storage.CodexCliAttachment{}, err
	}
	path := filepath.Join(dir, id+ext)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return storage.CodexCliAttachment{}, err
	}
	att, err := s.store.CreateCodexCliAttachment(ctx, storage.CodexCliAttachment{
		ThreadID:    threadID,
		Kind:        "image",
		Filename:    Preview(filepath.Base(filename), 120),
		ContentType: contentType,
		SizeBytes:   int64(len(data)),
		StoragePath: path,
		ExpiresAt:   time.Now().UTC().Add(attachmentTTL).Format(time.RFC3339Nano),
	})
	if err != nil {
		_ = os.Remove(path)
		return storage.CodexCliAttachment{}, err
	}
	return att, nil
}

func (s *Service) DeleteAttachment(ctx context.Context, id string) error {
	att, err := s.store.GetCodexCliAttachment(ctx, id)
	if err != nil {
		return err
	}
	if att.StoragePath != "" {
		_ = os.Remove(att.StoragePath)
	}
	return s.store.DeleteCodexCliAttachment(ctx, id)
}

func (s *Service) attachmentPaths(ctx context.Context, threadID string, ids []string) ([]string, error) {
	if len(ids) > MaxAttachmentsPerReq {
		return nil, fmt.Errorf("too many attachments: max %d", MaxAttachmentsPerReq)
	}
	paths := make([]string, 0, len(ids))
	for _, id := range ids {
		if strings.TrimSpace(id) == "" {
			continue
		}
		att, err := s.store.GetCodexCliAttachment(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("attachment not found: %s", id)
		}
		if att.ThreadID != threadID {
			return nil, fmt.Errorf("attachment does not belong to thread: %s", id)
		}
		if att.ExpiresAt != "" && att.ExpiresAt < time.Now().UTC().Format(time.RFC3339Nano) {
			return nil, fmt.Errorf("attachment expired: %s", id)
		}
		paths = append(paths, att.StoragePath)
	}
	return paths, nil
}

func (s *Service) cleanupAttachments(ctx context.Context) {
	expired, err := s.store.ListExpiredCodexCliAttachments(ctx, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return
	}
	for _, att := range expired {
		if att.StoragePath != "" {
			_ = os.Remove(att.StoragePath)
		}
		_ = s.store.DeleteCodexCliAttachment(ctx, att.ID)
	}
}

// cleanupExpiredEvents removes thread events older than the configured retention
// window. A retention of 0 disables time-based cleanup (only the per-thread
// count cap applies).
func (s *Service) cleanupExpiredEvents(ctx context.Context) {
	days := s.currentSettings().EventRetentionDays
	if days <= 0 {
		return
	}
	cutoff := time.Now().UTC().Add(-time.Duration(days) * 24 * time.Hour).Format(time.RFC3339Nano)
	removed, err := s.store.DeleteCodexCliEventsOlderThan(ctx, cutoff)
	if err != nil {
		s.log.Warn("codex cli event retention cleanup failed", "summary", Redact(err.Error(), 120))
		return
	}
	if removed > 0 {
		s.log.Info("codex cli event retention cleanup", "removed", removed, "retentionDays", days)
	}
}

// ---- diagnostics ----

func (s *Service) LegacyTables(ctx context.Context) ([]string, error) {
	return s.store.CodexCliLegacyTablesDetected(ctx)
}

// ---- helpers ----

func (s *Service) completeTurn(ctx context.Context, thread storage.CodexCliThread, turn storage.CodexCliTurn, usage map[string]any) {
	turn.Status = "completed"
	turn.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if usage != nil {
		turn.Usage = usage
	}
	_, _ = s.store.SaveCodexCliTurn(ctx, turn)
	thread.Status = "idle"
	_, _ = s.store.SaveCodexCliThread(ctx, thread)
	s.appendThreadEvent(ctx, thread.ID, turn.ID, EventTurnCompleted, "codex", "", "", nil)
}

func (s *Service) failTurn(ctx context.Context, thread storage.CodexCliThread, turn storage.CodexCliTurn, message string) {
	turn.Status = "failed"
	turn.ErrorSummary = message
	turn.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
	_, _ = s.store.SaveCodexCliTurn(ctx, turn)
	thread.Status = "failed"
	thread.LastError = message
	_, _ = s.store.SaveCodexCliThread(ctx, thread)
	s.appendThreadEvent(ctx, thread.ID, turn.ID, EventTurnFailed, "codex", "", message, nil)
}

func (s *Service) clearActiveTurn(turnID string) {
	s.mu.Lock()
	if s.activeTurn != nil && s.activeTurn.turnID == turnID {
		s.activeTurn = nil
	}
	s.mu.Unlock()
}

func turnRisk(policy RunPolicy) string {
	if policy.Sandbox == "workspace-write" || policy.NetworkEnabled {
		return "medium"
	}
	return "low"
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func limitEventPayload(payload map[string]any, maxBytes int) map[string]any {
	if maxBytes <= 0 {
		return payload
	}
	data, err := json.Marshal(payload)
	if err != nil || len(data) <= maxBytes {
		return payload
	}
	return map[string]any{
		"_truncated":    true,
		"originalBytes": len(data),
		"preview":       Redact(string(data), max(256, maxBytes/4)),
	}
}

func storageNewAttachmentID() (string, error) {
	return ids.New("cxatt")
}
