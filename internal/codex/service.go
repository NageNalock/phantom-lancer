package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"phantom-lancer/internal/events"
	"phantom-lancer/internal/storage"
)

type Service struct {
	Binary    string
	CodexHome string
	Store     *storage.Store
	Hub       *events.Hub

	mu              sync.Mutex
	cancels         map[string]context.CancelFunc
	app             *appServerClient
	threadSessions  map[string]string
	activeThreads   map[string]bool
	pendingRequests map[string]*pendingServerRequest
}

type pendingServerRequest struct {
	App       *appServerClient
	Request   appServerRequest
	SessionID string
	CreatedAt time.Time
}

type Status struct {
	Available          bool   `json:"available"`
	BinaryPath         string `json:"binaryPath,omitempty"`
	Version            string `json:"version,omitempty"`
	ExecAvailable      bool   `json:"execAvailable"`
	AppServerAvailable bool   `json:"appServerAvailable"`
	CodexHome          string `json:"codexHome,omitempty"`
	Error              string `json:"error,omitempty"`
}

type SessionOptions struct {
	Model             string
	ServiceTier       string
	ApprovalPolicy    string
	ApprovalsReviewer string
}

type threadResponse struct {
	Thread                map[string]any `json:"thread"`
	Model                 string         `json:"model"`
	ModelProvider         string         `json:"modelProvider"`
	ServiceTier           string         `json:"serviceTier"`
	Cwd                   string         `json:"cwd"`
	RuntimeWorkspaceRoots []string       `json:"runtimeWorkspaceRoots"`
	InstructionSources    []string       `json:"instructionSources"`
	ApprovalPolicy        any            `json:"approvalPolicy"`
	ApprovalsReviewer     string         `json:"approvalsReviewer"`
	ReasoningEffort       any            `json:"reasoningEffort"`
}

func NewService(binary, codexHome string, store *storage.Store, hub *events.Hub) *Service {
	binary = strings.TrimSpace(binary)
	if binary == "" {
		binary = "codex"
	}
	return &Service{
		Binary:          binary,
		CodexHome:       strings.TrimSpace(codexHome),
		Store:           store,
		Hub:             hub,
		cancels:         make(map[string]context.CancelFunc),
		threadSessions:  make(map[string]string),
		activeThreads:   make(map[string]bool),
		pendingRequests: make(map[string]*pendingServerRequest),
	}
}

func (s *Service) Configure(binary, codexHome string) {
	binary = strings.TrimSpace(binary)
	if binary == "" {
		binary = "codex"
	}
	codexHome = strings.TrimSpace(codexHome)

	s.mu.Lock()
	if s.Binary == binary && s.CodexHome == codexHome {
		s.mu.Unlock()
		return
	}
	app := s.app
	s.app = nil
	s.activeThreads = make(map[string]bool)
	s.pendingRequests = make(map[string]*pendingServerRequest)
	s.Binary = binary
	s.CodexHome = codexHome
	s.mu.Unlock()

	if app != nil {
		app.Close()
	}
	s.interruptPendingApprovals("Codex app-server 配置已变更")
}

func (s *Service) Status(ctx context.Context) Status {
	binary, codexHome := s.configSnapshot()
	path, err := exec.LookPath(binary)
	if err != nil {
		return Status{Available: false, CodexHome: codexHome, Error: err.Error()}
	}
	status := Status{Available: true, BinaryPath: path, CodexHome: codexHome}
	status.Version = strings.TrimSpace(runShort(ctx, path, "--version"))
	status.ExecAvailable = runShort(ctx, path, "exec", "--help") != ""
	status.AppServerAvailable = runShort(ctx, path, "app-server", "--help") != ""
	return status
}

func (s *Service) StartExecJob(parent context.Context, job storage.ExecJob, workspace storage.Workspace, prompt, sandbox string) {
	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.cancels[job.ID] = cancel
	s.mu.Unlock()

	go func() {
		defer func() {
			s.mu.Lock()
			delete(s.cancels, job.ID)
			s.mu.Unlock()
			cancel()
		}()
		select {
		case <-parent.Done():
			return
		default:
		}
		s.runExec(ctx, job, workspace, prompt, sandbox)
	}()
}

func (s *Service) Interrupt(jobID string) bool {
	s.mu.Lock()
	cancel := s.cancels[jobID]
	s.mu.Unlock()
	if cancel == nil {
		return false
	}
	cancel()
	return true
}

func (s *Service) Close() {
	s.mu.Lock()
	app := s.app
	s.app = nil
	s.pendingRequests = make(map[string]*pendingServerRequest)
	s.mu.Unlock()
	if app != nil {
		app.Close()
	}
	s.interruptPendingApprovals("Codex app-server 已关闭")
}

func (s *Service) CreateSession(ctx context.Context, workspace storage.Workspace, title, sandbox string, options SessionOptions) (storage.CodexSession, error) {
	if title == "" {
		title = "新的 Codex 会话"
	}
	session, err := s.Store.CreateCodexSession(ctx, workspace.ID, title, sandbox)
	if err != nil {
		return storage.CodexSession{}, err
	}
	if options.ApprovalPolicy == "" {
		options.ApprovalPolicy = session.ApprovalPolicy
	}
	if options.ApprovalsReviewer == "" {
		options.ApprovalsReviewer = session.ApprovalsReviewer
	}
	runtimeRoots := []string{}
	if workspace.RootPath != "" {
		runtimeRoots = []string{workspace.RootPath}
	}
	_ = s.Store.UpdateCodexSessionSettings(ctx, session.ID, storage.CodexSessionSettings{
		Model:             &options.Model,
		ServiceTier:       &options.ServiceTier,
		ApprovalPolicy:    &options.ApprovalPolicy,
		ApprovalsReviewer: &options.ApprovalsReviewer,
		Cwd:               &workspace.RootPath,
		RuntimeRoots:      &runtimeRoots,
	})
	session, _ = s.Store.GetCodexSession(ctx, session.ID)
	s.appendSession(ctx, session.ID, "session.created", map[string]any{
		"workspaceId": workspace.ID,
		"title":       session.Title,
		"sandbox":     session.Sandbox,
		"model":       session.Model,
	})
	session, err = s.startThread(ctx, session, workspace)
	if err != nil {
		_ = s.Store.UpdateCodexSessionStatus(context.Background(), session.ID, "failed")
		s.appendSession(context.Background(), session.ID, "session.failed", map[string]any{"message": err.Error()})
		return session, err
	}
	return session, nil
}

func (s *Service) SendTurn(ctx context.Context, session storage.CodexSession, workspace storage.Workspace, prompt, mode string) (storage.CodexTurn, string, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return storage.CodexTurn{}, "", errors.New("提示词不能为空")
	}
	session, err := s.ensureThread(ctx, session, workspace)
	if err != nil {
		return storage.CodexTurn{}, "", err
	}
	if mode == "" {
		mode = "auto"
	}
	if (mode == "auto" || mode == "steer") && session.Status == "active" && session.LastTurnID != "" {
		err := s.steerTurn(ctx, session, prompt)
		if err != nil {
			s.appendSession(context.Background(), session.ID, "turn.steer.failed", map[string]any{"message": err.Error()})
			return storage.CodexTurn{}, "steer", err
		}
		s.appendSession(context.Background(), session.ID, "turn.steered", map[string]any{
			"turnId":        session.LastTurnID,
			"promptPreview": previewText(prompt, 160),
		})
		return storage.CodexTurn{SessionID: session.ID, CodexTurnID: session.LastTurnID, PromptPreview: previewText(prompt, 160), Status: "running"}, "steer", nil
	}
	turn, err := s.startTurn(ctx, session, workspace, prompt)
	if err != nil {
		s.appendSession(context.Background(), session.ID, "turn.start.failed", map[string]any{"message": err.Error()})
		return storage.CodexTurn{}, "start", err
	}
	return turn, "start", nil
}

func (s *Service) InterruptSessionTurn(ctx context.Context, session storage.CodexSession) (bool, error) {
	if session.CodexThreadID == "" || session.LastTurnID == "" {
		return false, nil
	}
	app, err := s.ensureAppServer(ctx)
	if err != nil {
		return false, err
	}
	callCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	err = app.Call(callCtx, "turn/interrupt", map[string]any{
		"threadId": session.CodexThreadID,
		"turnId":   session.LastTurnID,
	}, nil)
	if err != nil {
		return false, err
	}
	s.appendSession(context.Background(), session.ID, "turn.interrupt.requested", map[string]any{"turnId": session.LastTurnID})
	return true, nil
}

func (s *Service) ArchiveSession(ctx context.Context, session storage.CodexSession) error {
	if session.CodexThreadID != "" {
		app, err := s.ensureAppServer(ctx)
		if err == nil {
			callCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
			_ = app.Call(callCtx, "thread/archive", map[string]any{"threadId": session.CodexThreadID}, nil)
			cancel()
		}
	}
	if err := s.Store.ArchiveCodexSession(ctx, session.ID, true); err != nil {
		return err
	}
	s.appendSession(ctx, session.ID, "thread.archived.local", map[string]any{"sessionId": session.ID})
	return nil
}

func (s *Service) UpdateSessionSettings(ctx context.Context, session storage.CodexSession, workspace storage.Workspace, model, serviceTier, approvalPolicy, approvalsReviewer, sandbox string) (storage.CodexSession, error) {
	if session.CodexThreadID == "" {
		return session, errors.New("会话还没有 Codex thread")
	}
	if model == "" {
		model = session.Model
	}
	if serviceTier == "" {
		serviceTier = session.ServiceTier
	}
	if approvalPolicy == "" {
		approvalPolicy = session.ApprovalPolicy
	}
	if approvalsReviewer == "" {
		approvalsReviewer = session.ApprovalsReviewer
	}
	if sandbox == "" {
		sandbox = session.Sandbox
	}
	app, err := s.ensureAppServer(ctx)
	if err != nil {
		return session, err
	}
	params := map[string]any{
		"threadId":          session.CodexThreadID,
		"approvalPolicy":    approvalPolicy,
		"approvalsReviewer": approvalsReviewer,
		"sandboxPolicy":     sandboxPolicy(sandbox, workspace.RootPath),
	}
	if model != "" {
		params["model"] = model
	}
	if serviceTier != "" {
		params["serviceTier"] = serviceTier
	}
	if workspace.RootPath != "" {
		params["cwd"] = workspace.RootPath
	}
	callCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	if err := app.Call(callCtx, "thread/settings/update", params, nil); err != nil {
		return session, err
	}
	runtimeRoots := []string{}
	if workspace.RootPath != "" {
		runtimeRoots = []string{workspace.RootPath}
	}
	update := storage.CodexSessionSettings{
		Model:             &model,
		ServiceTier:       &serviceTier,
		ApprovalPolicy:    &approvalPolicy,
		ApprovalsReviewer: &approvalsReviewer,
		Sandbox:           &sandbox,
		Cwd:               &workspace.RootPath,
		RuntimeRoots:      &runtimeRoots,
	}
	if err := s.Store.UpdateCodexSessionSettings(ctx, session.ID, update); err != nil {
		return session, err
	}
	s.appendSession(context.Background(), session.ID, "thread.settings.updated.local", map[string]any{
		"threadId":          session.CodexThreadID,
		"model":             model,
		"serviceTier":       serviceTier,
		"approvalPolicy":    approvalPolicy,
		"approvalsReviewer": approvalsReviewer,
		"sandbox":           sandbox,
	})
	return s.Store.GetCodexSession(ctx, session.ID)
}

func (s *Service) ListModels(ctx context.Context, includeHidden bool) (map[string]any, error) {
	app, err := s.ensureAppServer(ctx)
	if err != nil {
		return nil, err
	}
	callCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	var response map[string]any
	err = app.Call(callCtx, "model/list", map[string]any{"includeHidden": includeHidden, "limit": 200}, &response)
	return response, err
}

func (s *Service) Capability(ctx context.Context, method string, params map[string]any) (map[string]any, error) {
	app, err := s.ensureAppServer(ctx)
	if err != nil {
		return nil, err
	}
	callCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	var response map[string]any
	err = app.Call(callCtx, method, params, &response)
	return response, err
}

func (s *Service) ForkSession(ctx context.Context, session storage.CodexSession, workspace storage.Workspace) (storage.CodexSession, error) {
	if session.CodexThreadID == "" {
		return storage.CodexSession{}, errors.New("会话还没有 Codex thread")
	}
	app, err := s.ensureAppServer(ctx)
	if err != nil {
		return storage.CodexSession{}, err
	}
	callCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	params := threadParams(session, workspace)
	params["threadId"] = session.CodexThreadID
	params["excludeTurns"] = true
	var response threadResponse
	if err := app.Call(callCtx, "thread/fork", params, &response); err != nil {
		return storage.CodexSession{}, err
	}
	threadID := stringFromMap(response.Thread, "id")
	if threadID == "" {
		return storage.CodexSession{}, errors.New("Codex app-server 未返回 fork thread id")
	}
	title := strings.TrimSpace(session.Title)
	if title == "" {
		title = "Forked Codex 会话"
	} else {
		title += " fork"
	}
	forked, err := s.Store.CreateCodexSession(ctx, session.WorkspaceID, title, session.Sandbox)
	if err != nil {
		return storage.CodexSession{}, err
	}
	if err := s.Store.AttachCodexThread(ctx, forked.ID, threadID, "idle"); err != nil {
		return storage.CodexSession{}, err
	}
	_ = s.Store.UpdateCodexSessionSettings(ctx, forked.ID, storage.CodexSessionSettings{
		Model:             &session.Model,
		ModelProvider:     &session.ModelProvider,
		ServiceTier:       &session.ServiceTier,
		ApprovalPolicy:    &session.ApprovalPolicy,
		ApprovalsReviewer: &session.ApprovalsReviewer,
		PermissionProfile: &session.PermissionProfile,
		ReasoningEffort:   &session.ReasoningEffort,
		ReasoningSummary:  &session.ReasoningSummary,
		Sandbox:           &session.Sandbox,
		Cwd:               &workspace.RootPath,
	})
	s.applyThreadResponseSettings(context.Background(), forked.ID, response, workspace)
	s.registerThread(threadID, forked.ID)
	s.appendSession(context.Background(), forked.ID, "thread.forked.local", map[string]any{"threadId": threadID, "sourceSessionId": session.ID})
	return s.Store.GetCodexSession(ctx, forked.ID)
}

func (s *Service) RollbackSession(ctx context.Context, session storage.CodexSession, numTurns int) error {
	if session.CodexThreadID == "" {
		return errors.New("会话还没有 Codex thread")
	}
	if numTurns <= 0 {
		numTurns = 1
	}
	app, err := s.ensureAppServer(ctx)
	if err != nil {
		return err
	}
	callCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	if err := app.Call(callCtx, "thread/rollback", map[string]any{"threadId": session.CodexThreadID, "numTurns": numTurns}, nil); err != nil {
		return err
	}
	s.appendSession(context.Background(), session.ID, "thread.rollback.requested", map[string]any{"threadId": session.CodexThreadID, "numTurns": numTurns})
	return nil
}

func (s *Service) CompactSession(ctx context.Context, session storage.CodexSession) error {
	if session.CodexThreadID == "" {
		return errors.New("会话还没有 Codex thread")
	}
	app, err := s.ensureAppServer(ctx)
	if err != nil {
		return err
	}
	callCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	if err := app.Call(callCtx, "thread/compact/start", map[string]any{"threadId": session.CodexThreadID}, nil); err != nil {
		return err
	}
	s.appendSession(context.Background(), session.ID, "thread.compact.requested", map[string]any{"threadId": session.CodexThreadID})
	return nil
}

func (s *Service) StartReview(ctx context.Context, session storage.CodexSession, target string) error {
	if session.CodexThreadID == "" {
		return errors.New("会话还没有 Codex thread")
	}
	if strings.TrimSpace(target) == "" {
		target = "uncommittedChanges"
	}
	app, err := s.ensureAppServer(ctx)
	if err != nil {
		return err
	}
	callCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	params := map[string]any{
		"threadId": session.CodexThreadID,
		"target":   map[string]any{"type": target},
		"delivery": "inline",
	}
	if err := app.Call(callCtx, "review/start", params, nil); err != nil {
		return err
	}
	s.appendSession(context.Background(), session.ID, "review.started.local", map[string]any{"threadId": session.CodexThreadID, "target": target})
	return nil
}

func (s *Service) ResolveApproval(ctx context.Context, approval storage.CodexApproval, action string, payload map[string]any) error {
	action = normalizeApprovalAction(action)
	s.mu.Lock()
	pending := s.pendingRequests[approval.RequestID]
	s.mu.Unlock()
	if pending == nil || pending.App == nil || !pending.App.Running() {
		return errors.New("Codex app-server approval request 已不可用")
	}
	result, responseErr := approvalResponse(approval.RequestType, action, payload, approval.Request)
	if err := pending.App.Respond(approval.RequestID, result, responseErr); err != nil {
		return err
	}
	s.mu.Lock()
	if s.pendingRequests[approval.RequestID] == pending {
		delete(s.pendingRequests, approval.RequestID)
	}
	s.mu.Unlock()
	s.appendSession(context.Background(), approval.SessionID, "codex.approval.resolved", map[string]any{
		"requestId":   approval.RequestID,
		"requestType": approval.RequestType,
		"action":      action,
	})
	return nil
}

func (s *Service) startThread(ctx context.Context, session storage.CodexSession, workspace storage.Workspace) (storage.CodexSession, error) {
	app, err := s.ensureAppServer(ctx)
	if err != nil {
		return session, err
	}
	callCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	var response threadResponse
	if err := app.Call(callCtx, "thread/start", threadParams(session, workspace), &response); err != nil {
		return session, err
	}
	threadID := stringFromMap(response.Thread, "id")
	if threadID == "" {
		return session, errors.New("Codex app-server 未返回 thread id")
	}
	if err := s.Store.AttachCodexThread(ctx, session.ID, threadID, "idle"); err != nil {
		return session, err
	}
	session.CodexThreadID = threadID
	session.Status = "idle"
	s.applyThreadResponseSettings(context.Background(), session.ID, response, workspace)
	s.registerThread(threadID, session.ID)
	s.appendSession(context.Background(), session.ID, "thread.attached", map[string]any{"threadId": threadID})
	return session, nil
}

func (s *Service) ensureThread(ctx context.Context, session storage.CodexSession, workspace storage.Workspace) (storage.CodexSession, error) {
	if session.CodexThreadID == "" {
		return s.startThread(ctx, session, workspace)
	}
	s.mapThread(session.CodexThreadID, session.ID)
	if s.isThreadActive(session.CodexThreadID) {
		return session, nil
	}
	app, err := s.ensureAppServer(ctx)
	if err != nil {
		return session, err
	}
	callCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	var response threadResponse
	params := threadParams(session, workspace)
	params["threadId"] = session.CodexThreadID
	params["excludeTurns"] = true
	if err := app.Call(callCtx, "thread/resume", params, &response); err != nil {
		return session, err
	}
	s.applyThreadResponseSettings(context.Background(), session.ID, response, workspace)
	s.markThreadActive(session.CodexThreadID)
	s.appendSession(context.Background(), session.ID, "thread.resumed", map[string]any{"threadId": session.CodexThreadID})
	return session, nil
}

func (s *Service) startTurn(ctx context.Context, session storage.CodexSession, workspace storage.Workspace, prompt string) (storage.CodexTurn, error) {
	app, err := s.ensureAppServer(ctx)
	if err != nil {
		return storage.CodexTurn{}, err
	}
	callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var response struct {
		Turn map[string]any `json:"turn"`
	}
	params := turnParams(session, workspace, prompt)
	if err := app.Call(callCtx, "turn/start", params, &response); err != nil {
		return storage.CodexTurn{}, err
	}
	turnID := stringFromMap(response.Turn, "id")
	if turnID == "" {
		return storage.CodexTurn{}, errors.New("Codex app-server 未返回 turn id")
	}
	turn, err := s.Store.CreateCodexTurn(ctx, session.ID, turnID, previewText(prompt, 160))
	if err != nil {
		return storage.CodexTurn{}, err
	}
	_ = s.Store.UpdateCodexSessionActivity(context.Background(), session.ID, "active", turnID, turn.PromptPreview)
	s.appendSession(context.Background(), session.ID, "turn.submitted", map[string]any{
		"turnId":        turnID,
		"promptPreview": turn.PromptPreview,
	})
	return turn, nil
}

func (s *Service) steerTurn(ctx context.Context, session storage.CodexSession, prompt string) error {
	app, err := s.ensureAppServer(ctx)
	if err != nil {
		return err
	}
	callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var response struct {
		TurnID string `json:"turnId"`
	}
	return app.Call(callCtx, "turn/steer", map[string]any{
		"threadId":       session.CodexThreadID,
		"expectedTurnId": session.LastTurnID,
		"input":          []map[string]any{{"type": "text", "text": prompt, "text_elements": []any{}}},
	}, &response)
}

func (s *Service) ensureAppServer(ctx context.Context) (*appServerClient, error) {
	s.mu.Lock()
	if s.app != nil && s.app.Running() {
		app := s.app
		s.mu.Unlock()
		return app, nil
	}
	s.app = nil
	s.activeThreads = make(map[string]bool)
	s.pendingRequests = make(map[string]*pendingServerRequest)
	s.mu.Unlock()
	s.interruptPendingApprovals("Codex app-server 已重启")

	binary, codexHome := s.configSnapshot()
	app, err := startAppServer(ctx, binary, envWithCodexHome(codexHome))
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	if s.Binary != binary || s.CodexHome != codexHome {
		s.mu.Unlock()
		app.Close()
		return s.ensureAppServer(ctx)
	}
	s.app = app
	s.mu.Unlock()
	go s.consumeAppServerNotifications(app)
	go s.consumeAppServerRequests(app)
	return app, nil
}

func (s *Service) consumeAppServerNotifications(app *appServerClient) {
	for notification := range app.Notifications() {
		s.handleAppServerNotification(notification)
	}
}

func (s *Service) consumeAppServerRequests(app *appServerClient) {
	for request := range app.ServerRequests() {
		s.handleAppServerRequest(app, request)
	}
}

func (s *Service) handleAppServerNotification(notification appServerNotification) {
	threadID := threadIDFromParams(notification.Params)
	if threadID == "" {
		return
	}
	sessionID := s.sessionIDForThread(threadID)
	if sessionID == "" {
		session, err := s.Store.GetCodexSessionByThreadID(context.Background(), threadID)
		if err != nil {
			return
		}
		sessionID = session.ID
		s.registerThread(threadID, sessionID)
	}
	s.appendSession(context.Background(), sessionID, notification.Method, notification.Params)
	s.persistItemFromNotification(context.Background(), sessionID, notification.Method, notification.Params)
	switch notification.Method {
	case "thread/started":
		s.markThreadActive(threadID)
	case "thread/status/changed":
		_ = s.Store.UpdateCodexSessionStatus(context.Background(), sessionID, statusFromAny(notification.Params["status"]))
	case "thread/archived":
		_ = s.Store.ArchiveCodexSession(context.Background(), sessionID, true)
	case "turn/started":
		if turnID := turnIDFromParams(notification.Params); turnID != "" {
			_ = s.Store.UpdateCodexSessionStatus(context.Background(), sessionID, "active")
		}
	case "turn/completed":
		turnID := turnIDFromParams(notification.Params)
		status := turnStatusFromParams(notification.Params)
		if status == "" {
			status = "completed"
		}
		if turnID != "" {
			_ = s.Store.UpdateCodexTurn(context.Background(), sessionID, turnID, status, "")
		}
		_ = s.Store.UpdateCodexSessionStatus(context.Background(), sessionID, "idle")
	case "thread/settings/updated":
		s.updateSettingsFromNotification(sessionID, notification.Params)
	case "thread/tokenUsage/updated":
		_ = s.Store.UpdateCodexSessionTokenUsage(context.Background(), sessionID, mapFromAny(notification.Params["tokenUsage"]))
	}
}

func (s *Service) persistItemFromNotification(ctx context.Context, sessionID, method string, params map[string]any) {
	input, ok := itemInputFromNotification(sessionID, method, params)
	if !ok {
		return
	}
	_, _ = s.Store.UpsertCodexItem(ctx, input)
}

func (s *Service) handleAppServerRequest(app *appServerClient, request appServerRequest) {
	threadID := threadIDFromParams(request.Params)
	if threadID == "" {
		_ = app.Respond(request.ID, nil, &rpcError{Code: -32000, Message: "Phantom Lancer does not support this global Codex request yet"})
		return
	}
	sessionID := s.sessionIDForThread(threadID)
	if sessionID == "" {
		session, err := s.Store.GetCodexSessionByThreadID(context.Background(), threadID)
		if err != nil {
			_ = app.Respond(request.ID, nil, &rpcError{Code: -32000, Message: "Phantom Lancer could not map Codex request to a session"})
			return
		}
		sessionID = session.ID
		s.registerThread(threadID, sessionID)
	}
	if !supportedAppServerRequest(request.Method) {
		_ = app.Respond(request.ID, nil, &rpcError{Code: -32000, Message: "Unsupported Codex client request"})
		s.appendSession(context.Background(), sessionID, "codex.approval.unsupported", map[string]any{
			"requestId":   request.ID,
			"requestType": request.Method,
		})
		return
	}
	input := storage.CodexApprovalInput{
		SessionID:   sessionID,
		TurnID:      turnIDFromParams(request.Params),
		RequestID:   request.ID,
		RequestType: request.Method,
		Status:      "pending",
		RiskLevel:   approvalRisk(request.Method),
		Summary:     approvalSummary(request.Method, request.Params),
		Request: sanitizePayload(map[string]any{
			"method": request.Method,
			"params": request.Params,
		}),
		ExpiresAt: time.Now().UTC().Add(30 * time.Minute).Format(time.RFC3339),
	}
	approval, err := s.Store.CreateCodexApproval(context.Background(), input)
	if err != nil {
		_ = app.Respond(request.ID, nil, &rpcError{Code: -32000, Message: "Phantom Lancer failed to persist Codex approval"})
		return
	}
	s.mu.Lock()
	s.pendingRequests[request.ID] = &pendingServerRequest{App: app, Request: request, SessionID: sessionID, CreatedAt: time.Now().UTC()}
	s.mu.Unlock()
	s.appendSession(context.Background(), sessionID, "codex.approval.requested", map[string]any{
		"approvalId":  approval.ID,
		"requestId":   approval.RequestID,
		"requestType": approval.RequestType,
		"riskLevel":   approval.RiskLevel,
		"summary":     approval.Summary,
	})
	go s.expireApproval(app, approval)
}

func (s *Service) expireApproval(app *appServerClient, approval storage.CodexApproval) {
	timer := time.NewTimer(30 * time.Minute)
	defer timer.Stop()
	<-timer.C
	s.mu.Lock()
	pending := s.pendingRequests[approval.RequestID]
	if pending == nil {
		s.mu.Unlock()
		return
	}
	delete(s.pendingRequests, approval.RequestID)
	s.mu.Unlock()
	_, _ = s.Store.ResolveCodexApproval(context.Background(), approval.ID, "expired", map[string]any{"action": "cancel"})
	if app != nil && app.Running() {
		result, responseErr := approvalResponse(approval.RequestType, "cancel", map[string]any{}, map[string]any{})
		_ = app.Respond(approval.RequestID, result, responseErr)
	}
	s.appendSession(context.Background(), approval.SessionID, "codex.approval.expired", map[string]any{"approvalId": approval.ID, "requestId": approval.RequestID})
}

func (s *Service) interruptPendingApprovals(reason string) {
	if s.Store == nil {
		return
	}
	approvals, err := s.Store.InterruptPendingCodexApprovals(context.Background(), reason)
	if err != nil {
		return
	}
	for _, approval := range approvals {
		s.appendSession(context.Background(), approval.SessionID, "codex.approval.interrupted", map[string]any{
			"approvalId":  approval.ID,
			"requestId":   approval.RequestID,
			"requestType": approval.RequestType,
			"reason":      reason,
		})
	}
}

func (s *Service) updateSettingsFromNotification(sessionID string, params map[string]any) {
	settings := mapFromAny(params["threadSettings"])
	if len(settings) == 0 {
		return
	}
	update := storage.CodexSessionSettings{}
	hasUpdate := false
	if value, ok := settings["model"]; ok {
		model := stringFromAny(value)
		update.Model = &model
		hasUpdate = true
	}
	if value, ok := settings["modelProvider"]; ok {
		modelProvider := stringFromAny(value)
		update.ModelProvider = &modelProvider
		hasUpdate = true
	}
	if value, ok := settings["serviceTier"]; ok {
		serviceTier := stringFromAny(value)
		update.ServiceTier = &serviceTier
		hasUpdate = true
	}
	if value, ok := settings["approvalPolicy"]; ok {
		approvalPolicy := approvalPolicyString(value)
		update.ApprovalPolicy = &approvalPolicy
		hasUpdate = true
	}
	if value, ok := settings["approvalsReviewer"]; ok {
		approvalsReviewer := stringFromAny(value)
		update.ApprovalsReviewer = &approvalsReviewer
		hasUpdate = true
	}
	if value, ok := settings["reasoningEffort"]; ok {
		reasoningEffort := stringFromAny(value)
		update.ReasoningEffort = &reasoningEffort
		hasUpdate = true
	} else if value, ok := settings["effort"]; ok {
		reasoningEffort := stringFromAny(value)
		update.ReasoningEffort = &reasoningEffort
		hasUpdate = true
	}
	if value, ok := settings["reasoningSummary"]; ok {
		reasoningSummary := stringFromAny(value)
		update.ReasoningSummary = &reasoningSummary
		hasUpdate = true
	} else if value, ok := settings["summary"]; ok {
		reasoningSummary := stringFromAny(value)
		update.ReasoningSummary = &reasoningSummary
		hasUpdate = true
	}
	if value, ok := settings["cwd"]; ok {
		cwd := stringFromAny(value)
		update.Cwd = &cwd
		hasUpdate = true
	}
	if hasUpdate {
		_ = s.Store.UpdateCodexSessionSettings(context.Background(), sessionID, update)
	}
}

func (s *Service) applyThreadResponseSettings(ctx context.Context, sessionID string, response threadResponse, workspace storage.Workspace) {
	cwd := response.Cwd
	if cwd == "" {
		cwd = workspace.RootPath
	}
	runtimeRoots := response.RuntimeWorkspaceRoots
	if len(runtimeRoots) == 0 && workspace.RootPath != "" {
		runtimeRoots = []string{workspace.RootPath}
	}
	update := storage.CodexSessionSettings{}
	if response.Model != "" {
		update.Model = &response.Model
	}
	if response.ModelProvider != "" {
		update.ModelProvider = &response.ModelProvider
	}
	if response.ServiceTier != "" {
		update.ServiceTier = &response.ServiceTier
	}
	if response.ApprovalPolicy != nil {
		approvalPolicy := approvalPolicyString(response.ApprovalPolicy)
		update.ApprovalPolicy = &approvalPolicy
	}
	if response.ApprovalsReviewer != "" {
		update.ApprovalsReviewer = &response.ApprovalsReviewer
	}
	if response.ReasoningEffort != nil {
		reasoningEffort := stringFromAny(response.ReasoningEffort)
		update.ReasoningEffort = &reasoningEffort
	}
	if cwd != "" {
		update.Cwd = &cwd
	}
	if len(runtimeRoots) > 0 {
		update.RuntimeRoots = &runtimeRoots
	}
	_ = s.Store.UpdateCodexSessionSettings(ctx, sessionID, update)
	if len(response.InstructionSources) > 0 {
		_ = s.Store.UpdateCodexSessionInstructionSources(ctx, sessionID, response.InstructionSources)
	}
}

func (s *Service) appendSession(ctx context.Context, sessionID, eventType string, payload map[string]any) {
	event, err := s.Store.AppendEvent(ctx, "codex_session", sessionID, eventType, sanitizePayload(payload))
	if err == nil {
		s.Hub.Publish(event)
	}
}

func (s *Service) registerThread(threadID, sessionID string) {
	s.mu.Lock()
	s.threadSessions[threadID] = sessionID
	s.activeThreads[threadID] = true
	s.mu.Unlock()
}

func (s *Service) mapThread(threadID, sessionID string) {
	s.mu.Lock()
	s.threadSessions[threadID] = sessionID
	s.mu.Unlock()
}

func (s *Service) markThreadActive(threadID string) {
	s.mu.Lock()
	s.activeThreads[threadID] = true
	s.mu.Unlock()
}

func (s *Service) isThreadActive(threadID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.activeThreads[threadID]
}

func (s *Service) sessionIDForThread(threadID string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.threadSessions[threadID]
}

func (s *Service) runExec(ctx context.Context, job storage.ExecJob, workspace storage.Workspace, prompt, sandbox string) {
	s.append(ctx, job.ID, "job.started", map[string]any{
		"workspaceId": workspace.ID,
		"sandbox":     sandbox,
	})

	binaryName, codexHome := s.configSnapshot()
	binary, err := exec.LookPath(binaryName)
	if err != nil {
		s.fail(ctx, job.ID, "codex_unavailable", err)
		return
	}

	args := []string{"exec", "--json", "--sandbox", sandbox, prompt}
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = workspace.RootPath
	cmd.Env = envWithCodexHome(codexHome)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		s.fail(ctx, job.ID, "stdout_pipe_failed", err)
		return
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		s.fail(ctx, job.ID, "stderr_pipe_failed", err)
		return
	}
	if err := cmd.Start(); err != nil {
		s.fail(ctx, job.ID, "start_failed", err)
		return
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			var payload map[string]any
			if err := json.Unmarshal([]byte(line), &payload); err != nil {
				payload = map[string]any{"line": line}
			}
			eventType := "codex.event"
			if value, ok := payload["type"].(string); ok && value != "" {
				eventType = value
			}
			s.append(ctx, job.ID, eventType, payload)
		}
	}()
	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stderr)
		scanner.Buffer(make([]byte, 0, 32*1024), 256*1024)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line != "" {
				s.append(ctx, job.ID, "process.stderr", map[string]any{"line": line})
			}
		}
	}()

	waitErr := cmd.Wait()
	wg.Wait()
	if errors.Is(ctx.Err(), context.Canceled) {
		_ = s.Store.UpdateExecJob(context.Background(), job.ID, "interrupted", "用户已中断")
		s.append(context.Background(), job.ID, "job.interrupted", map[string]any{"reason": "用户已中断"})
		return
	}
	if waitErr != nil {
		s.fail(context.Background(), job.ID, "process_failed", waitErr)
		return
	}
	_ = s.Store.UpdateExecJob(context.Background(), job.ID, "completed", "")
	s.append(context.Background(), job.ID, "job.completed", map[string]any{"status": "completed"})
}

func (s *Service) fail(ctx context.Context, jobID, code string, err error) {
	_ = s.Store.UpdateExecJob(context.Background(), jobID, "failed", err.Error())
	s.append(ctx, jobID, "job.failed", map[string]any{"code": code, "message": err.Error()})
}

func (s *Service) append(ctx context.Context, jobID, eventType string, payload map[string]any) {
	event, err := s.Store.AppendEvent(ctx, "exec_job", jobID, eventType, sanitizePayload(payload))
	if err == nil {
		s.Hub.Publish(event)
	}
}

func (s *Service) configSnapshot() (string, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Binary, s.CodexHome
}

func envWithCodexHome(codexHome string) []string {
	env := os.Environ()
	if codexHome != "" {
		env = append(env, "CODEX_HOME="+codexHome)
	}
	return env
}

func runShort(ctx context.Context, binary string, args ...string) string {
	timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(timeoutCtx, binary, args...).CombinedOutput()
	if err != nil {
		return ""
	}
	return string(out)
}

func threadParams(session storage.CodexSession, workspace storage.Workspace) map[string]any {
	approvalPolicy := session.ApprovalPolicy
	if approvalPolicy == "" {
		approvalPolicy = "on-request"
	}
	approvalsReviewer := session.ApprovalsReviewer
	if approvalsReviewer == "" {
		approvalsReviewer = "user"
	}
	params := map[string]any{
		"sandbox":           session.Sandbox,
		"approvalPolicy":    approvalPolicy,
		"approvalsReviewer": approvalsReviewer,
	}
	if session.Model != "" {
		params["model"] = session.Model
	}
	if session.ModelProvider != "" {
		params["modelProvider"] = session.ModelProvider
	}
	if session.ServiceTier != "" {
		params["serviceTier"] = session.ServiceTier
	}
	if workspace.RootPath != "" {
		params["cwd"] = workspace.RootPath
		params["runtimeWorkspaceRoots"] = []string{workspace.RootPath}
	}
	return params
}

func turnParams(session storage.CodexSession, workspace storage.Workspace, prompt string) map[string]any {
	approvalPolicy := session.ApprovalPolicy
	if approvalPolicy == "" {
		approvalPolicy = "on-request"
	}
	params := map[string]any{
		"threadId":       session.CodexThreadID,
		"input":          []map[string]any{{"type": "text", "text": prompt, "text_elements": []any{}}},
		"sandboxPolicy":  sandboxPolicy(session.Sandbox, workspace.RootPath),
		"approvalPolicy": approvalPolicy,
	}
	if session.Model != "" {
		params["model"] = session.Model
	}
	if session.ServiceTier != "" {
		params["serviceTier"] = session.ServiceTier
	}
	if workspace.RootPath != "" {
		params["cwd"] = workspace.RootPath
		params["runtimeWorkspaceRoots"] = []string{workspace.RootPath}
	}
	return params
}

func sandboxPolicy(sandbox, root string) map[string]any {
	if sandbox == "workspace-write" && root != "" {
		return map[string]any{
			"type":                "workspaceWrite",
			"writableRoots":       []string{root},
			"networkAccess":       false,
			"excludeTmpdirEnvVar": false,
			"excludeSlashTmp":     false,
		}
	}
	return map[string]any{
		"type":          "readOnly",
		"networkAccess": false,
	}
}

func stringFromMap(values map[string]any, key string) string {
	if values == nil {
		return ""
	}
	value, _ := values[key].(string)
	return value
}

func threadIDFromParams(params map[string]any) string {
	if value, ok := params["threadId"].(string); ok {
		return value
	}
	if thread, ok := params["thread"].(map[string]any); ok {
		return stringFromMap(thread, "id")
	}
	return ""
}

func turnIDFromParams(params map[string]any) string {
	if value, ok := params["turnId"].(string); ok {
		return value
	}
	if turn, ok := params["turn"].(map[string]any); ok {
		return stringFromMap(turn, "id")
	}
	return ""
}

func turnStatusFromParams(params map[string]any) string {
	if turn, ok := params["turn"].(map[string]any); ok {
		return stringFromMap(turn, "status")
	}
	return ""
}

func statusFromAny(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case map[string]any:
		if status := stringFromMap(typed, "type"); status != "" {
			return status
		}
	}
	return "idle"
}

func previewText(value string, max int) string {
	value = strings.TrimSpace(value)
	if len(value) <= max {
		return value
	}
	return value[:max] + "..."
}

func mapFromAny(value any) map[string]any {
	if typed, ok := value.(map[string]any); ok {
		return typed
	}
	return map[string]any{}
}

func stringFromAny(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case map[string]any:
		if value := stringFromMap(typed, "type"); value != "" {
			return value
		}
	}
	return ""
}

func approvalPolicyString(value any) string {
	if text := stringFromAny(value); text != "" {
		return text
	}
	if _, ok := value.(map[string]any); ok {
		return "granular"
	}
	return "on-request"
}

func itemInputFromNotification(sessionID, method string, params map[string]any) (storage.CodexItemInput, bool) {
	switch method {
	case "item/started", "item/completed", "rawResponseItem/completed", "turn/diff/updated", "turn/plan/updated", "item/fileChange/patchUpdated":
	default:
		return storage.CodexItemInput{}, false
	}
	item := mapFromAny(params["item"])
	if len(item) == 0 {
		item = mapFromAny(params["rawResponseItem"])
	}
	turnID := turnIDFromParams(params)
	itemID := stringFromAny(params["itemId"])
	if itemID == "" {
		itemID = stringFromAny(item["id"])
	}
	if itemID == "" && turnID != "" && (method == "turn/diff/updated" || method == "turn/plan/updated") {
		itemID = turnID + ":" + method
	}
	if itemID == "" && method == "item/fileChange/patchUpdated" {
		itemID = stringFromAny(params["changeId"])
	}
	itemType := codexItemType(method, item)
	payload := map[string]any{"method": method}
	if turnID != "" {
		payload["turnId"] = turnID
	}
	if len(item) > 0 {
		payload["item"] = item
	} else {
		payload["params"] = params
	}
	return storage.CodexItemInput{
		SessionID:   sessionID,
		TurnID:      turnID,
		CodexItemID: itemID,
		ItemType:    itemType,
		Status:      codexItemStatus(method, item),
		Title:       codexItemTitle(method, itemType, item, params),
		Summary:     codexItemSummary(item, params),
		Payload:     sanitizePayload(payload),
	}, true
}

func codexItemType(method string, item map[string]any) string {
	if value := stringFromAny(item["type"]); value != "" {
		return value
	}
	switch method {
	case "turn/diff/updated":
		return "diff"
	case "turn/plan/updated":
		return "plan"
	case "item/fileChange/patchUpdated":
		return "fileChange"
	case "rawResponseItem/completed":
		return "rawResponseItem"
	default:
		return "event"
	}
}

func codexItemStatus(method string, item map[string]any) string {
	if status := stringFromAny(item["status"]); status != "" {
		return status
	}
	if strings.Contains(method, "completed") {
		return "completed"
	}
	if strings.Contains(method, "failed") {
		return "failed"
	}
	return "running"
}

func codexItemTitle(method, itemType string, item, params map[string]any) string {
	switch itemType {
	case "userMessage":
		return "User message"
	case "agentMessage":
		return "Assistant message"
	case "commandExecution":
		if command := stringFromAny(item["command"]); command != "" {
			return "Command: " + previewText(redactText(command), 120)
		}
		return "Command execution"
	case "fileChange":
		if path := stringFromAny(item["path"]); path != "" {
			return "File change: " + previewText(path, 120)
		}
		if path := stringFromAny(params["path"]); path != "" {
			return "File change: " + previewText(path, 120)
		}
		return "File change"
	case "reasoning":
		return "Reasoning"
	case "diff":
		return "Workspace diff"
	case "plan":
		return "Plan"
	default:
		if itemType != "" && itemType != "event" {
			return itemType
		}
		return method
	}
}

func codexItemSummary(item, params map[string]any) string {
	for _, key := range []string{"text", "summary", "aggregatedOutput", "command", "path"} {
		if value := stringFromAny(item[key]); value != "" {
			return previewText(redactText(value), 1200)
		}
		if value := stringFromAny(params[key]); value != "" {
			return previewText(redactText(value), 1200)
		}
	}
	if content := contentSummary(item["content"]); content != "" {
		return content
	}
	if plan := contentSummary(params["plan"]); plan != "" {
		return plan
	}
	return ""
}

func contentSummary(value any) string {
	items, ok := value.([]any)
	if !ok {
		return ""
	}
	parts := []string{}
	for _, entry := range items {
		item := mapFromAny(entry)
		for _, key := range []string{"text", "path", "url", "title"} {
			if text := stringFromAny(item[key]); text != "" {
				parts = append(parts, redactText(text))
				break
			}
		}
		if len(parts) >= 8 {
			break
		}
	}
	return previewText(strings.Join(parts, "\n"), 1200)
}

func approvalRisk(method string) string {
	switch method {
	case "item/commandExecution/requestApproval", "execCommandApproval", "item/permissions/requestApproval":
		return "high"
	case "item/fileChange/requestApproval", "applyPatchApproval", "mcpServer/elicitation/request", "item/tool/requestUserInput":
		return "medium"
	default:
		return "medium"
	}
}

func supportedAppServerRequest(method string) bool {
	switch method {
	case "item/commandExecution/requestApproval",
		"item/fileChange/requestApproval",
		"item/tool/requestUserInput",
		"mcpServer/elicitation/request",
		"item/permissions/requestApproval":
		return true
	default:
		return false
	}
}

func approvalSummary(method string, params map[string]any) string {
	switch method {
	case "item/commandExecution/requestApproval", "execCommandApproval":
		command := stringFromAny(params["command"])
		if command == "" {
			return "Codex 请求执行命令"
		}
		return "Codex 请求执行命令：" + previewText(redactText(command), 120)
	case "item/fileChange/requestApproval", "applyPatchApproval":
		if root := stringFromAny(params["grantRoot"]); root != "" {
			return "Codex 请求写入目录：" + previewText(root, 120)
		}
		return "Codex 请求应用文件变更"
	case "item/permissions/requestApproval":
		if reason := stringFromAny(params["reason"]); reason != "" {
			return "Codex 请求权限提升：" + previewText(redactText(reason), 120)
		}
		return "Codex 请求权限提升"
	case "item/tool/requestUserInput":
		return "Codex 请求用户输入"
	case "mcpServer/elicitation/request":
		serverName := stringFromAny(params["serverName"])
		if serverName != "" {
			return "MCP server 请求确认：" + serverName
		}
		return "MCP server 请求确认"
	case "item/tool/call":
		return "Codex 请求调用动态工具"
	default:
		return "Codex 请求客户端确认：" + method
	}
}

func normalizeApprovalAction(action string) string {
	switch strings.TrimSpace(action) {
	case "allow", "accept":
		return "accept"
	case "allow_session", "allowForSession", "acceptForSession":
		return "acceptForSession"
	case "deny", "decline":
		return "decline"
	case "cancel":
		return "cancel"
	default:
		return "decline"
	}
}

func approvalResponse(requestType, action string, payload, request map[string]any) (any, *rpcError) {
	switch requestType {
	case "item/commandExecution/requestApproval", "execCommandApproval":
		return map[string]any{"decision": commandDecision(action, payload)}, nil
	case "item/fileChange/requestApproval", "applyPatchApproval":
		return map[string]any{"decision": fileDecision(action)}, nil
	case "mcpServer/elicitation/request":
		return map[string]any{"action": mcpAction(action), "content": payload["content"], "_meta": payload["_meta"]}, nil
	case "item/tool/requestUserInput":
		answers := payload["answers"]
		if answers == nil {
			answers = map[string]any{}
		}
		return map[string]any{"answers": answers}, nil
	case "item/permissions/requestApproval":
		if action == "accept" || action == "acceptForSession" {
			params := mapFromAny(request["params"])
			permissions := mapFromAny(params["permissions"])
			return map[string]any{
				"permissions": permissionGrant(permissions),
				"scope":       map[string]string{"accept": "turn", "acceptForSession": "session"}[action],
			}, nil
		}
		return nil, &rpcError{Code: -32000, Message: "Permission request declined"}
	default:
		return nil, &rpcError{Code: -32000, Message: "Unsupported Codex client request"}
	}
}

func commandDecision(action string, payload map[string]any) any {
	if action == "accept" || action == "acceptForSession" || action == "decline" || action == "cancel" {
		return action
	}
	if value := payload["decision"]; value != nil {
		return value
	}
	return "decline"
}

func fileDecision(action string) string {
	if action == "accept" || action == "acceptForSession" || action == "decline" || action == "cancel" {
		return action
	}
	return "decline"
}

func mcpAction(action string) string {
	switch action {
	case "accept", "acceptForSession":
		return "accept"
	case "decline", "cancel":
		return action
	}
	return "decline"
}

func permissionGrant(request map[string]any) map[string]any {
	grant := map[string]any{}
	if network, ok := request["network"]; ok && network != nil {
		grant["network"] = network
	}
	if fileSystem, ok := request["fileSystem"]; ok && fileSystem != nil {
		grant["fileSystem"] = fileSystem
	}
	return grant
}

func sanitizePayload(payload map[string]any) map[string]any {
	sanitized := redactValue(payload).(map[string]any)
	return sanitized
}

func redactValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			if sensitiveKey(key) {
				out[key] = "[redacted]"
				continue
			}
			out[key] = redactValue(item)
		}
		return out
	case []any:
		limit := len(typed)
		if limit > 100 {
			limit = 100
		}
		out := make([]any, 0, limit)
		for i := 0; i < limit; i++ {
			out = append(out, redactValue(typed[i]))
		}
		return out
	case string:
		redacted := redactText(typed)
		if len(redacted) > 4000 {
			return redacted[:4000] + "...[truncated]"
		}
		return redacted
	default:
		return typed
	}
}

var secretTextPatterns = []struct {
	pattern     *regexp.Regexp
	replacement string
}{
	{regexp.MustCompile(`(?i)(authorization:\s*bearer\s+)[^\s]+`), `${1}[redacted]`},
	{regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9._~+/=-]{8,}`), `${1}[redacted]`},
	{regexp.MustCompile(`(?i)((?:password|token|secret|api[_-]?key)=)[^\s&]+`), `${1}[redacted]`},
	{regexp.MustCompile(`sk-[A-Za-z0-9_-]{8,}`), `[redacted]`},
}

func redactText(value string) string {
	for _, item := range secretTextPatterns {
		value = item.pattern.ReplaceAllString(value, item.replacement)
	}
	return value
}

func sensitiveKey(key string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(key, "_", ""), "-", ""))
	for _, marker := range []string{"apikey", "authorization", "cookie", "csrftoken", "sessiontoken", "password", "secret", "accesstoken", "refreshtoken", "privatekey", "presigned"} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}
