package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
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

	mu             sync.Mutex
	cancels        map[string]context.CancelFunc
	app            *appServerClient
	threadSessions map[string]string
	activeThreads  map[string]bool
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

func NewService(binary, codexHome string, store *storage.Store, hub *events.Hub) *Service {
	binary = strings.TrimSpace(binary)
	if binary == "" {
		binary = "codex"
	}
	return &Service{
		Binary:         binary,
		CodexHome:      strings.TrimSpace(codexHome),
		Store:          store,
		Hub:            hub,
		cancels:        make(map[string]context.CancelFunc),
		threadSessions: make(map[string]string),
		activeThreads:  make(map[string]bool),
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
	s.Binary = binary
	s.CodexHome = codexHome
	s.mu.Unlock()

	if app != nil {
		app.Close()
	}
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
	s.mu.Unlock()
	if app != nil {
		app.Close()
	}
}

func (s *Service) CreateSession(ctx context.Context, workspace storage.Workspace, title, sandbox string) (storage.CodexSession, error) {
	if title == "" {
		title = "新的 Codex 会话"
	}
	session, err := s.Store.CreateCodexSession(ctx, workspace.ID, title, sandbox)
	if err != nil {
		return storage.CodexSession{}, err
	}
	s.appendSession(ctx, session.ID, "session.created", map[string]any{
		"workspaceId": workspace.ID,
		"title":       session.Title,
		"sandbox":     session.Sandbox,
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

func (s *Service) startThread(ctx context.Context, session storage.CodexSession, workspace storage.Workspace) (storage.CodexSession, error) {
	app, err := s.ensureAppServer(ctx)
	if err != nil {
		return session, err
	}
	callCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	var response struct {
		Thread map[string]any `json:"thread"`
	}
	if err := app.Call(callCtx, "thread/start", map[string]any{
		"cwd":                   workspace.RootPath,
		"runtimeWorkspaceRoots": []string{workspace.RootPath},
		"sandbox":               session.Sandbox,
		"approvalPolicy":        "never",
	}, &response); err != nil {
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
	var response struct {
		Thread map[string]any `json:"thread"`
	}
	if err := app.Call(callCtx, "thread/resume", map[string]any{
		"threadId":              session.CodexThreadID,
		"cwd":                   workspace.RootPath,
		"runtimeWorkspaceRoots": []string{workspace.RootPath},
		"sandbox":               session.Sandbox,
		"approvalPolicy":        "never",
		"excludeTurns":          true,
	}, &response); err != nil {
		return session, err
	}
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
	if err := app.Call(callCtx, "turn/start", map[string]any{
		"threadId":              session.CodexThreadID,
		"input":                 []map[string]any{{"type": "text", "text": prompt, "text_elements": []any{}}},
		"cwd":                   workspace.RootPath,
		"runtimeWorkspaceRoots": []string{workspace.RootPath},
		"sandboxPolicy":         sandboxPolicy(session.Sandbox, workspace.RootPath),
		"approvalPolicy":        "never",
	}, &response); err != nil {
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
	s.mu.Unlock()

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
	return app, nil
}

func (s *Service) consumeAppServerNotifications(app *appServerClient) {
	for notification := range app.Notifications() {
		s.handleAppServerNotification(notification)
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
	}
}

func (s *Service) appendSession(ctx context.Context, sessionID, eventType string, payload map[string]any) {
	event, err := s.Store.AppendEvent(ctx, "codex_session", sessionID, eventType, payload)
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
	event, err := s.Store.AppendEvent(ctx, "exec_job", jobID, eventType, payload)
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

func sandboxPolicy(sandbox, root string) map[string]any {
	if sandbox == "workspace-write" {
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
