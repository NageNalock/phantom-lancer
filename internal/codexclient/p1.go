package codexclient

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"phantom-lancer/internal/storage"

	xhtml "golang.org/x/net/html"
)

const (
	commandTimeout          = 10 * time.Minute
	commandPreviewRunes     = 4000
	commandOutputEventLimit = 120
	reviewMaxBytes          = 256 * 1024
	browserPreviewMaxBytes  = 512 * 1024
	browserResourceMaxBytes = 2 * 1024 * 1024
)

var cssURLPattern = regexp.MustCompile(`(?i)url\(\s*(['"]?)([^'")\s][^'")]*)['"]?\s*\)`)

type queuedTurnInput struct {
	prompt string
	model  string
	policy RunPolicy
	images []string
}

type QueueStatus struct {
	MaxConcurrent   int                    `json:"maxConcurrent"`
	Running         int                    `json:"running"`
	WaitingApproval int                    `json:"waitingApproval"`
	Failed          int                    `json:"failed"`
	Queued          []storage.CodexCliTurn `json:"queued"`
}

type ReviewSnapshot struct {
	Scope       string                          `json:"scope"`
	Workspace   storage.CodexCliWorkspace       `json:"workspace"`
	Summary     string                          `json:"summary"`
	Diff        string                          `json:"diff"`
	Truncated   bool                            `json:"truncated"`
	Comments    []storage.CodexCliReviewComment `json:"comments"`
	GeneratedAt string                          `json:"generatedAt"`
}

type ReviewCommentInput struct {
	FilePath   string `json:"filePath"`
	OldLine    int    `json:"oldLine"`
	NewLine    int    `json:"newLine"`
	HunkHeader string `json:"hunkHeader"`
	Body       string `json:"body"`
}

type CommandInput struct {
	Command       string   `json:"command"`
	Args          []string `json:"args"`
	ConfirmDanger bool     `json:"confirmDanger"`
}

type CommandAssessment struct {
	Class                string `json:"class"`
	RiskSummary          string `json:"riskSummary"`
	RequiresConfirmation bool   `json:"requiresConfirmation"`
	SandboxSummary       string `json:"sandboxSummary"`
	CwdSummary           string `json:"cwdSummary"`
	CommandPreview       string `json:"commandPreview"`
}

type BrowserSessionInput struct {
	URL         string `json:"url"`
	AllowPublic bool   `json:"allowPublic"`
}

type BrowserCommentInput struct {
	Body string `json:"body"`
	X    int    `json:"x"`
	Y    int    `json:"y"`
}

type BrowserPreview struct {
	ContentType  string
	Body         []byte
	ScriptPolicy string
}

func (s *Service) activeForPayload(payload map[string]any) *appTurnContext {
	turnID := firstString(payload, "turnId", "turn_id")
	if turn, ok := payload["turn"].(map[string]any); ok && turnID == "" {
		turnID = firstString(turn, "id", "turnId", "turn_id")
	}
	threadID := firstString(payload, "threadId", "thread_id")
	if thread, ok := payload["thread"].(map[string]any); ok && threadID == "" {
		threadID = firstString(thread, "id", "threadId", "thread_id")
	}
	itemID := activePayloadItemID(payload)
	s.mu.RLock()
	defer s.mu.RUnlock()
	if turnID != "" {
		for _, active := range s.activeTurns {
			if active.turnID == turnID || active.codexTurnID == turnID {
				return active
			}
		}
	}
	if threadID != "" {
		for _, active := range s.activeTurns {
			if active.threadID == threadID || active.codexThreadID == threadID {
				return active
			}
		}
	}
	if itemID != "" {
		if activeTurnID := s.activeItemTurns[itemID]; activeTurnID != "" {
			return s.activeTurns[activeTurnID]
		}
	}
	if len(s.activeTurns) == 1 {
		for _, active := range s.activeTurns {
			return active
		}
	}
	return nil
}

func activePayloadItemID(payload map[string]any) string {
	itemID := firstString(payload, "itemId", "item_id", "callId", "call_id")
	if item, ok := payload["item"].(map[string]any); ok && itemID == "" {
		itemID = firstString(item, "id", "itemId", "item_id", "callId", "call_id")
	}
	if request, ok := payload["request"].(map[string]any); ok && itemID == "" {
		itemID = firstString(request, "itemId", "item_id", "callId", "call_id")
	}
	return itemID
}

func (s *Service) rememberActiveItem(turnID string, payload map[string]any) {
	itemID := activePayloadItemID(payload)
	if itemID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.activeTurns[turnID] != nil {
		s.activeItemTurns[itemID] = turnID
	}
}

func (s *Service) dispatchPreparedTurn(ctx context.Context, thread storage.CodexCliThread, ws storage.CodexCliWorkspace, turn storage.CodexCliTurn, prompt, model string, policy RunPolicy, images []string) (storage.CodexCliTurn, error) {
	client := s.supervisor.Client()
	if client != nil && thread.SourceMode == "app_server" {
		go s.runAppServerTurn(context.WithoutCancel(ctx), client, thread, ws, turn, prompt, model, policy, images)
		return turn, nil
	}
	if !s.currentSettings().ExecFallbackEnabled {
		s.failTurn(ctx, thread, turn, "app-server unavailable and exec fallback disabled")
		return turn, ErrNoRunner
	}
	if policy.Sandbox != "read-only" {
		s.failTurn(ctx, thread, turn, "exec fallback requires read-only sandbox")
		return turn, errors.New("exec fallback requires read-only sandbox")
	}
	go s.runExecTurn(context.WithoutCancel(ctx), thread, ws, turn, prompt, model, policy, images)
	return turn, nil
}

func (s *Service) QueueStatus(ctx context.Context) (QueueStatus, error) {
	counts, err := s.store.CountCodexCliTurnsByStatus(ctx)
	if err != nil {
		return QueueStatus{}, err
	}
	queued, err := s.store.ListQueuedCodexCliTurns(ctx, 100)
	if err != nil {
		return QueueStatus{}, err
	}
	return QueueStatus{
		MaxConcurrent:   s.currentSettings().MaxConcurrentTurns,
		Running:         counts["running"],
		WaitingApproval: counts["waiting_approval"],
		Failed:          counts["failed"],
		Queued:          queued,
	}, nil
}

func (s *Service) drainTurnQueue(ctx context.Context) {
	s.turnScheduleMu.Lock()
	defer s.turnScheduleMu.Unlock()
	for {
		running, err := s.store.CountRunningCodexCliTurns(ctx)
		if err != nil || running >= s.currentSettings().MaxConcurrentTurns {
			return
		}
		queued, err := s.store.ListQueuedCodexCliTurns(ctx, 10)
		if err != nil || len(queued) == 0 {
			return
		}
		dispatched := false
		for _, turn := range queued {
			input, ok := s.takeQueuedInput(turn.ID)
			if !ok {
				s.failUnresumableQueuedTurn(ctx, turn, "queued turn cannot be resumed after server restart")
				dispatched = true
				continue
			}
			thread, err := s.store.GetCodexCliThread(ctx, turn.ThreadID)
			if err != nil {
				continue
			}
			if workspaceHasActiveTurn(ctx, s.store, thread.WorkspaceID) {
				s.rememberQueuedInput(turn.ID, input)
				continue
			}
			ws, err := s.store.GetCodexCliWorkspace(ctx, thread.WorkspaceID)
			if err != nil {
				continue
			}
			turn.Status = "running"
			if saved, err := s.store.SaveCodexCliTurn(ctx, turn); err == nil {
				turn = saved
			}
			thread.Status = "running"
			thread.LastError = ""
			_, _ = s.store.SaveCodexCliThread(ctx, thread)
			s.appendThreadEvent(ctx, thread.ID, turn.ID, EventTurnStarted, "codex", "", "", map[string]any{"model": input.model, "sandbox": input.policy.Sandbox, "approval": input.policy.ApprovalPolicy, "queued": true})
			_, _ = s.store.AddAudit(ctx, storage.AuditEvent{EventType: "codex_cli.turn.started", WorkspaceID: thread.WorkspaceID, RiskLevel: turnRisk(input.policy), Summary: "已从队列启动 Codex turn", Payload: map[string]any{"threadId": thread.ID, "turnId": turn.ID}})
			_, _ = s.dispatchPreparedTurn(ctx, thread, ws, turn, input.prompt, input.model, input.policy, input.images)
			dispatched = true
			break
		}
		if !dispatched {
			return
		}
	}
}

func (s *Service) failUnresumableQueuedTurn(ctx context.Context, turn storage.CodexCliTurn, message string) {
	thread, err := s.store.GetCodexCliThread(ctx, turn.ThreadID)
	if err != nil {
		return
	}
	turn.Status = "failed"
	turn.ErrorSummary = message
	turn.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
	_, _ = s.store.SaveCodexCliTurn(ctx, turn)
	thread.Status = "failed"
	thread.LastError = message
	_, _ = s.store.SaveCodexCliThread(ctx, thread)
	s.appendThreadEvent(ctx, thread.ID, turn.ID, EventTurnFailed, "codex", "", message, map[string]any{"reason": "queue_input_missing"})
	_, _ = s.store.AddAudit(ctx, storage.AuditEvent{EventType: "codex_cli.turn.failed", WorkspaceID: thread.WorkspaceID, RiskLevel: "medium", Summary: "Codex queued turn 无法恢复", Payload: map[string]any{"threadId": thread.ID, "turnId": turn.ID, "reason": "queue_input_missing"}})
}

func (s *Service) rememberQueuedInput(turnID string, input queuedTurnInput) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.queuedInputs[turnID] = input
}

func (s *Service) takeQueuedInput(turnID string) (queuedTurnInput, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	input, ok := s.queuedInputs[turnID]
	if ok {
		delete(s.queuedInputs, turnID)
	}
	return input, ok
}

func workspaceHasQueuedOrActiveTurn(ctx context.Context, store *storage.Store, workspaceID string) bool {
	return workspaceHasLiveTurn(ctx, store, workspaceID, true)
}

func workspaceHasActiveTurn(ctx context.Context, store *storage.Store, workspaceID string) bool {
	return workspaceHasLiveTurn(ctx, store, workspaceID, false)
}

func workspaceHasLiveTurn(ctx context.Context, store *storage.Store, workspaceID string, includeQueued bool) bool {
	threads, err := store.ListCodexCliThreads(ctx, false, "")
	if err != nil {
		return true
	}
	for _, thread := range threads {
		if thread.WorkspaceID != workspaceID {
			continue
		}
		turns, err := store.ListCodexCliTurns(ctx, thread.ID)
		if err != nil {
			return true
		}
		for _, turn := range turns {
			if turn.Status == "running" || turn.Status == "waiting_approval" || (includeQueued && turn.Status == "queued") {
				return true
			}
		}
	}
	return false
}

// ---- Git review ----

func (s *Service) ReviewSnapshot(ctx context.Context, threadID, scope string) (ReviewSnapshot, error) {
	thread, ws, err := s.threadWorkspace(ctx, threadID)
	if err != nil {
		return ReviewSnapshot{}, err
	}
	_, gitState := readGitHeadSummary(ws.Path)
	if gitState == "none" {
		return ReviewSnapshot{}, errors.New("workspace is not a git repository")
	}
	scope = strings.TrimSpace(scope)
	if scope == "" {
		scope = "uncommitted"
	}
	var summaryArgs []string
	var diffArgs []string
	var emptySummary string
	switch scope {
	case "uncommitted":
		summaryArgs = []string{"diff", "--no-ext-diff", "--stat", "HEAD", "--", "."}
		diffArgs = []string{"diff", "--no-ext-diff", "HEAD", "--", "."}
	case "last_turn":
		files, err := s.lastTurnReviewFiles(ctx, thread, ws)
		if err != nil {
			return ReviewSnapshot{}, err
		}
		if len(files) == 0 {
			emptySummary = "No file changes were recorded for the last turn."
		} else {
			summaryArgs = append([]string{"diff", "--no-ext-diff", "--stat", "HEAD", "--"}, files...)
			diffArgs = append([]string{"diff", "--no-ext-diff", "HEAD", "--"}, files...)
		}
	case "branch":
		base := branchReviewBase(ctx, ws.Path)
		summaryArgs = []string{"diff", "--no-ext-diff", "--stat", base + "...HEAD", "--", "."}
		diffArgs = []string{"diff", "--no-ext-diff", base + "...HEAD", "--", "."}
	default:
		return ReviewSnapshot{}, errors.New("unsupported review scope")
	}
	summary := emptySummary
	diff := ""
	truncated := false
	diffTruncated := false
	if len(summaryArgs) > 0 {
		summary, truncated, err = runBoundedGit(ctx, ws.Path, summaryArgs, 64*1024)
		if err != nil {
			return ReviewSnapshot{}, err
		}
		diff, diffTruncated, err = runBoundedGit(ctx, ws.Path, diffArgs, reviewMaxBytes)
		if err != nil {
			return ReviewSnapshot{}, err
		}
	}
	comments, err := s.store.ListCodexCliReviewComments(ctx, thread.ID)
	if err != nil {
		return ReviewSnapshot{}, err
	}
	return ReviewSnapshot{Scope: scope, Workspace: enrichWorkspaceGitSummary(ws), Summary: summary, Diff: diff, Truncated: truncated || diffTruncated, Comments: comments, GeneratedAt: time.Now().UTC().Format(time.RFC3339Nano)}, nil
}

func (s *Service) CreateReviewComment(ctx context.Context, threadID string, input ReviewCommentInput) (storage.CodexCliReviewComment, error) {
	thread, ws, err := s.threadWorkspace(ctx, threadID)
	if err != nil {
		return storage.CodexCliReviewComment{}, err
	}
	filePath, err := safeRepoPath(ws.Path, input.FilePath)
	if err != nil {
		return storage.CodexCliReviewComment{}, err
	}
	body := Preview(input.Body, 2000)
	if strings.TrimSpace(body) == "" {
		return storage.CodexCliReviewComment{}, errors.New("comment body is required")
	}
	comment, err := s.store.CreateCodexCliReviewComment(ctx, storage.CodexCliReviewComment{
		ThreadID:    thread.ID,
		TurnID:      thread.LastTurnID,
		WorkspaceID: ws.ID,
		FilePath:    filePath,
		OldLine:     input.OldLine,
		NewLine:     input.NewLine,
		HunkHeader:  Preview(input.HunkHeader, 300),
		Body:        body,
	})
	if err != nil {
		return storage.CodexCliReviewComment{}, err
	}
	s.appendThreadEvent(ctx, thread.ID, thread.LastTurnID, "review.comment.created", "codex", "review", body, map[string]any{"commentId": comment.ID, "filePath": filePath})
	_, _ = s.store.AddAudit(ctx, storage.AuditEvent{EventType: "codex_cli.review.comment_created", WorkspaceID: ws.ID, RiskLevel: "low", Summary: "已添加 Codex review comment", Payload: map[string]any{"threadId": thread.ID, "commentId": comment.ID}})
	return comment, nil
}

func (s *Service) ResolveReviewComment(ctx context.Context, id string) (storage.CodexCliReviewComment, error) {
	return s.store.ResolveCodexCliReviewComment(ctx, id)
}

func runBoundedGit(ctx context.Context, cwd string, args []string, maxBytes int) (string, bool, error) {
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, "git", args...)
	cmd.Dir = cwd
	cmd.Env = append(BuildChildEnv(""), "GIT_PAGER=cat", "PAGER=cat", "GIT_EXTERNAL_DIFF=")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", false, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", false, err
	}
	if err := cmd.Start(); err != nil {
		return "", false, err
	}
	outCh := make(chan boundedReadResult, 1)
	errCh := make(chan boundedReadResult, 1)
	go readBounded(stdout, maxBytes, outCh)
	go readBounded(stderr, 4*1024, errCh)
	waitErr := cmd.Wait()
	out := <-outCh
	errOut := <-errCh
	if out.err != nil {
		return "", false, out.err
	}
	if waitErr != nil {
		message := strings.TrimSpace(string(errOut.data))
		if message == "" {
			message = waitErr.Error()
		}
		return "", false, errors.New(Preview(message, 300))
	}
	return redactMultiline(string(out.data), maxBytes), out.truncated, nil
}

type boundedReadResult struct {
	data      []byte
	truncated bool
	err       error
}

func readBounded(r io.Reader, maxBytes int, ch chan<- boundedReadResult) {
	data, err := io.ReadAll(io.LimitReader(r, int64(maxBytes)+1))
	if err != nil {
		ch <- boundedReadResult{err: err}
		return
	}
	truncated := len(data) > maxBytes
	if truncated {
		data = data[:maxBytes]
	}
	ch <- boundedReadResult{data: data, truncated: truncated}
}

func safeRepoPath(workspacePath, value string) (string, error) {
	workspacePath = resolvedWorkspacePath(workspacePath)
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("file path is required")
	}
	cleaned := filepath.Clean(value)
	if cleaned == "." || filepath.IsAbs(cleaned) || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) || cleaned == ".." {
		return "", errors.New("file path must be repository-relative")
	}
	full := filepath.Join(workspacePath, cleaned)
	if resolved, err := filepath.EvalSymlinks(full); err == nil {
		if !isInsidePath(workspacePath, resolved) {
			return "", errors.New("file path escapes workspace")
		}
	} else {
		parent := deepestExistingParent(workspacePath, filepath.Dir(full))
		if resolvedParent, perr := filepath.EvalSymlinks(parent); perr == nil && !isInsidePath(workspacePath, resolvedParent) {
			return "", errors.New("file path escapes workspace")
		}
	}
	return filepath.ToSlash(cleaned), nil
}

func deepestExistingParent(workspacePath, parent string) string {
	parent = filepath.Clean(parent)
	workspacePath = resolvedWorkspacePath(workspacePath)
	for isInsidePath(workspacePath, parent) {
		if _, err := os.Lstat(parent); err == nil {
			return parent
		}
		next := filepath.Dir(parent)
		if next == parent {
			break
		}
		parent = next
	}
	return workspacePath
}

func resolvedWorkspacePath(path string) string {
	if resolved, err := filepath.EvalSymlinks(filepath.Clean(path)); err == nil {
		return resolved
	}
	return filepath.Clean(path)
}

func (s *Service) lastTurnReviewFiles(ctx context.Context, thread storage.CodexCliThread, ws storage.CodexCliWorkspace) ([]string, error) {
	if strings.TrimSpace(thread.LastTurnID) == "" {
		return nil, nil
	}
	events, err := s.store.ListCodexCliEvents(ctx, thread.ID, 0, 1000)
	if err != nil {
		return nil, err
	}
	files := map[string]struct{}{}
	for _, event := range events {
		if event.TurnID != thread.LastTurnID {
			continue
		}
		if event.EventType != EventFileChangeStart && event.EventType != EventFileChangeDone && event.EventType != EventDiffUpdated {
			continue
		}
		collectReviewPaths(ws.Path, event.Payload, files)
	}
	out := make([]string, 0, len(files))
	for file := range files {
		out = append(out, file)
	}
	sort.Strings(out)
	return out, nil
}

func collectReviewPaths(workspacePath string, value any, out map[string]struct{}) {
	switch typed := value.(type) {
	case map[string]any:
		for key, raw := range typed {
			lower := strings.ToLower(key)
			if lower == "path" || lower == "filepath" || lower == "file_path" {
				if text, ok := raw.(string); ok {
					if rel, err := safeRepoPath(workspacePath, text); err == nil {
						out[rel] = struct{}{}
					}
				}
			}
			collectReviewPaths(workspacePath, raw, out)
		}
	case []any:
		for _, item := range typed {
			collectReviewPaths(workspacePath, item, out)
		}
	}
}

func branchReviewBase(ctx context.Context, cwd string) string {
	if upstream, _, err := runBoundedGit(ctx, cwd, []string{"rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}"}, 4096); err == nil {
		if upstream = strings.TrimSpace(upstream); upstream != "" {
			return upstream
		}
	}
	if base, _, err := runBoundedGit(ctx, cwd, []string{"merge-base", "HEAD", "origin/HEAD"}, 4096); err == nil {
		if base = strings.TrimSpace(base); base != "" {
			return base
		}
	}
	return "HEAD"
}

func redactMultiline(message string, maxRunes int) string {
	message = strings.TrimSpace(message)
	for _, pattern := range secretPatterns {
		message = pattern.ReplaceAllString(message, "${1}[redacted]")
	}
	if maxRunes > 0 {
		runes := []rune(message)
		if len(runes) > maxRunes {
			message = string(runes[:maxRunes]) + "..."
		}
	}
	return message
}

// ---- Command runner ----

func (s *Service) ListCommands(ctx context.Context, threadID string) ([]storage.CodexCliCommand, error) {
	return s.store.ListCodexCliCommands(ctx, threadID)
}

func (s *Service) AssessCommand(ctx context.Context, threadID string, input CommandInput) (CommandAssessment, error) {
	_, ws, err := s.threadWorkspace(ctx, threadID)
	if err != nil {
		return CommandAssessment{}, err
	}
	argv, err := normalizeCommand(input)
	if err != nil {
		return CommandAssessment{}, err
	}
	assessment, err := assessOwnerCommand(argv)
	if err != nil {
		return CommandAssessment{}, err
	}
	return CommandAssessment{
		Class:                assessment.Class,
		RiskSummary:          assessment.RiskSummary,
		RequiresConfirmation: assessment.RequiresConfirmation,
		SandboxSummary:       commandSandboxSummary(assessment),
		CwdSummary:           ws.PathSummary,
		CommandPreview:       Preview(strings.Join(argv, " "), 500),
	}, nil
}

func (s *Service) RunCommand(ctx context.Context, threadID string, input CommandInput) (storage.CodexCliCommand, error) {
	thread, ws, err := s.threadWorkspace(ctx, threadID)
	if err != nil {
		return storage.CodexCliCommand{}, err
	}
	argv, err := normalizeCommand(input)
	if err != nil {
		return storage.CodexCliCommand{}, err
	}
	commandPreview := Preview(strings.Join(argv, " "), 500)
	assessment, err := assessOwnerCommand(argv)
	if err != nil {
		return storage.CodexCliCommand{}, err
	}
	if assessment.RequiresConfirmation && !input.ConfirmDanger {
		return storage.CodexCliCommand{}, errors.New("command requires explicit confirmation: " + assessment.RiskSummary)
	}
	command, err := s.store.CreateCodexCliCommand(ctx, storage.CodexCliCommand{
		ThreadID:       thread.ID,
		WorkspaceID:    ws.ID,
		CommandPreview: commandPreview,
		CwdSummary:     ws.PathSummary,
		Status:         "queued",
	})
	if err != nil {
		return storage.CodexCliCommand{}, err
	}
	s.appendThreadEvent(ctx, thread.ID, thread.LastTurnID, "command.owner.queued", "owner", "command", commandPreview, map[string]any{"commandId": command.ID, "riskSummary": assessment.RiskSummary, "sandbox": commandSandboxSummary(assessment), "outputAttached": false})
	_, _ = s.store.AddAudit(ctx, storage.AuditEvent{EventType: "codex_cli.command.queued", WorkspaceID: ws.ID, RiskLevel: commandRiskLevel(assessment), Summary: "Owner queued Codex command", Payload: map[string]any{"threadId": thread.ID, "commandId": command.ID, "class": assessment.Class, "sandbox": commandSandboxSummary(assessment)}})
	go s.runOwnerCommand(context.Background(), command, ws, argv)
	return command, nil
}

func (s *Service) InterruptCommand(ctx context.Context, id string) (storage.CodexCliCommand, error) {
	current, err := s.store.GetCodexCliCommand(ctx, id)
	if err != nil {
		return storage.CodexCliCommand{}, err
	}
	if current.Status != "queued" && current.Status != "running" {
		return current, nil
	}
	s.mu.Lock()
	cancel := s.commandCancels[id]
	delete(s.commandCancels, id)
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return s.store.FinishCodexCliCommand(ctx, id, "cancelled", 130, "", "interrupted by owner")
}

func (s *Service) AttachCommandOutput(ctx context.Context, id string) (storage.CodexCliCommand, error) {
	command, err := s.store.GetCodexCliCommand(ctx, id)
	if err != nil {
		return storage.CodexCliCommand{}, err
	}
	if strings.TrimSpace(command.OutputPreview) == "" {
		return storage.CodexCliCommand{}, errors.New("command has no output preview to attach")
	}
	s.appendThreadEvent(ctx, command.ThreadID, "", "command.owner.output.attached", "owner", "command", command.OutputPreview, map[string]any{"commandId": command.ID, "status": command.Status, "exitCode": command.ExitCode, "outputAttached": true})
	_, _ = s.store.AddAudit(ctx, storage.AuditEvent{EventType: "codex_cli.command.output_attached", WorkspaceID: command.WorkspaceID, RiskLevel: "low", Summary: "Owner attached command output to Codex thread", Payload: map[string]any{"threadId": command.ThreadID, "commandId": command.ID}})
	return command, nil
}

func (s *Service) runOwnerCommand(ctx context.Context, command storage.CodexCliCommand, ws storage.CodexCliWorkspace, argv []string) {
	// Register the cancel handle before transitioning out of queued so an
	// interrupt that lands while the runner is starting always reaches a live
	// cancel func instead of a nil one.
	runCtx, cancel := context.WithTimeout(ctx, commandTimeout)
	s.mu.Lock()
	s.commandCancels[command.ID] = cancel
	s.mu.Unlock()
	defer func() {
		cancel()
		s.mu.Lock()
		delete(s.commandCancels, command.ID)
		s.mu.Unlock()
	}()

	started, transitioned, err := s.store.StartCodexCliCommand(ctx, command.ID)
	if err != nil {
		s.log.Warn("codex owner command start failed", "summary", Redact(err.Error(), 120), "commandId", command.ID)
		return
	}
	// The command was cancelled or otherwise resolved before the runner could
	// start it (for example an interrupt that landed while it was still queued).
	// Leave it in its terminal state and do not execute.
	if !transitioned {
		return
	}
	command = started
	assessment, err := assessOwnerCommand(argv)
	if err != nil {
		s.finishOwnerCommand(ctx, command, "failed", 1, "", Redact(err.Error(), 200))
		return
	}
	spec, err := s.ownerCommandRunSpec(command.ID, ws, argv, assessment)
	if err != nil {
		s.finishOwnerCommand(ctx, command, "failed", 1, "", Redact(err.Error(), 200))
		return
	}
	s.appendThreadEvent(ctx, command.ThreadID, "", "command.owner.started", "owner", "command", command.CommandPreview, map[string]any{"commandId": command.ID, "sandbox": spec.SandboxSummary})

	cmd := exec.CommandContext(runCtx, spec.Argv[0], spec.Argv[1:]...)
	cmd.Dir = spec.Dir
	cmd.Env = spec.Env
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		s.finishOwnerCommand(ctx, command, "failed", 1, "", Redact(err.Error(), 200))
		return
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		s.finishOwnerCommand(ctx, command, "failed", 1, "", Redact(err.Error(), 200))
		return
	}
	if err := cmd.Start(); err != nil {
		s.finishOwnerCommand(ctx, command, "failed", 1, "", Redact(err.Error(), 200))
		return
	}
	output := &strings.Builder{}
	var outputMu sync.Mutex
	eventMu := sync.Mutex{}
	emittedOutputEvents := 0
	truncationNotified := false
	done := make(chan struct{}, 2)
	stream := func(name string, r io.Reader) {
		scanner := bufio.NewScanner(r)
		scanner.Buffer(make([]byte, 0, 64*1024), 512*1024)
		for scanner.Scan() {
			text := Preview(Redact(scanner.Text(), 2000), 2000)
			outputMu.Lock()
			if output.Len() < commandPreviewRunes {
				output.WriteString(text)
				output.WriteByte('\n')
			}
			outputMu.Unlock()
			eventMu.Lock()
			switch {
			case emittedOutputEvents < commandOutputEventLimit:
				emittedOutputEvents++
				eventMu.Unlock()
				s.appendThreadEvent(ctx, command.ThreadID, "", "command.owner.output", "owner", "command", text, map[string]any{"commandId": command.ID, "stream": name, "outputAttached": false})
			case !truncationNotified:
				truncationNotified = true
				eventMu.Unlock()
				s.appendThreadEvent(ctx, command.ThreadID, "", "command.owner.output", "owner", "command", "command output event limit reached; remaining output kept only in bounded preview", map[string]any{"commandId": command.ID, "stream": name, "truncated": true, "outputAttached": false})
			default:
				eventMu.Unlock()
			}
		}
		if err := scanner.Err(); err != nil {
			text := Preview(Redact(name+" stream read stopped: "+err.Error(), 300), 300)
			outputMu.Lock()
			if output.Len() < commandPreviewRunes {
				output.WriteString(text)
				output.WriteByte('\n')
			}
			outputMu.Unlock()
			s.appendThreadEvent(ctx, command.ThreadID, "", "command.owner.output", "owner", "command", text, map[string]any{"commandId": command.ID, "stream": name, "truncated": true, "outputAttached": false})
		}
		done <- struct{}{}
	}
	go stream("stdout", stdout)
	go stream("stderr", stderr)
	<-done
	<-done
	err = cmd.Wait()
	exitCode := 0
	status := "completed"
	errorSummary := ""
	if err != nil {
		status = "failed"
		exitCode = 1
		if exit, ok := err.(*exec.ExitError); ok {
			exitCode = exit.ExitCode()
		}
		errorSummary = Redact(err.Error(), 200)
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			status = "timeout"
			errorSummary = "command timed out"
		} else if errors.Is(runCtx.Err(), context.Canceled) {
			status = "cancelled"
			errorSummary = "interrupted by owner"
		}
	}
	s.finishOwnerCommand(ctx, command, status, exitCode, Preview(output.String(), commandPreviewRunes), errorSummary)
}

func (s *Service) finishOwnerCommand(ctx context.Context, command storage.CodexCliCommand, status string, exitCode int, outputPreview string, errorSummary string) {
	saved, _ := s.store.FinishCodexCliCommand(ctx, command.ID, status, exitCode, outputPreview, errorSummary)
	s.appendThreadEvent(ctx, command.ThreadID, "", "command.owner.completed", "owner", "command", commandCompletionPreview(status, exitCode, saved.ErrorSummary), map[string]any{"commandId": command.ID, "status": status, "exitCode": exitCode, "outputAttached": false})
}

type ownerCommandRunSpec struct {
	Argv           []string
	Dir            string
	Env            []string
	SandboxSummary string
}

func (s *Service) ownerCommandRunSpec(commandID string, ws storage.CodexCliWorkspace, argv []string, assessment ownerCommandAssessment) (ownerCommandRunSpec, error) {
	cacheRoot := s.commandCacheRoot(commandID)
	env := s.commandEnv(cacheRoot)
	runArgv := argv
	if assessment.Class == "git-read" {
		runArgv = controlledGitArgv(argv)
	}
	spec := ownerCommandRunSpec{
		Argv:           runArgv,
		Dir:            ws.Path,
		Env:            env,
		SandboxSummary: commandSandboxSummary(assessment),
	}
	if !assessment.NeedsSandbox {
		return spec, nil
	}
	sandboxArgv, err := buildOwnerCommandSandboxArgv(runArgv, ws.Path, cacheRoot)
	if err != nil {
		return ownerCommandRunSpec{}, err
	}
	spec.Argv = sandboxArgv
	return spec, nil
}

func commandSandboxSummary(assessment ownerCommandAssessment) string {
	if assessment.NeedsSandbox {
		return "OS sandbox: network disabled; writes limited to workspace and Phantom command cache"
	}
	return "read-only allowlisted command surface"
}

func buildOwnerCommandSandboxArgv(argv []string, workspacePath, cacheRoot string) ([]string, error) {
	switch runtime.GOOS {
	case "darwin":
		return buildDarwinCommandSandboxArgv(argv, workspacePath, cacheRoot)
	case "linux":
		return buildLinuxCommandSandboxArgv(argv, workspacePath, cacheRoot)
	default:
		return nil, errors.New("project commands require an OS sandbox; unsupported platform " + runtime.GOOS)
	}
}

func buildDarwinCommandSandboxArgv(argv []string, workspacePath, cacheRoot string) ([]string, error) {
	exe, err := exec.LookPath("sandbox-exec")
	if err != nil {
		return nil, errors.New("project commands require sandbox-exec on macOS")
	}
	profile := darwinCommandSandboxProfile(workspacePath, cacheRoot)
	out := []string{exe, "-p", profile, "--"}
	out = append(out, argv...)
	return out, nil
}

func darwinCommandSandboxProfile(workspacePath, cacheRoot string) string {
	readPaths := []string{"/bin", "/sbin", "/usr", "/System", "/Library", "/opt", "/private/var", workspacePath, cacheRoot}
	writePaths := []string{workspacePath, cacheRoot}
	var b strings.Builder
	b.WriteString("(version 1)\n")
	b.WriteString("(deny default)\n")
	b.WriteString("(allow process*)\n")
	b.WriteString("(allow signal (target self))\n")
	b.WriteString("(allow sysctl-read)\n")
	b.WriteString("(allow mach-lookup)\n")
	b.WriteString("(allow file-read-metadata)\n")
	for _, path := range readPaths {
		if path == "" {
			continue
		}
		b.WriteString("(allow file-read* (subpath \"")
		b.WriteString(escapeSandboxPath(path))
		b.WriteString("\"))\n")
	}
	for _, path := range writePaths {
		if path == "" {
			continue
		}
		b.WriteString("(allow file-write* (subpath \"")
		b.WriteString(escapeSandboxPath(path))
		b.WriteString("\"))\n")
	}
	for _, path := range []string{"/dev/null", "/dev/zero", "/dev/random", "/dev/urandom"} {
		b.WriteString("(allow file-read* file-write* (literal \"")
		b.WriteString(path)
		b.WriteString("\"))\n")
	}
	b.WriteString("(deny network*)\n")
	return b.String()
}

func buildLinuxCommandSandboxArgv(argv []string, workspacePath, cacheRoot string) ([]string, error) {
	exe, err := exec.LookPath("bwrap")
	if err != nil {
		return nil, errors.New("project commands require bwrap on Linux")
	}
	args := []string{exe, "--die-with-parent", "--unshare-net", "--proc", "/proc", "--dev", "/dev"}
	for _, path := range []string{"/usr", "/bin", "/sbin", "/lib", "/lib64", "/opt", "/etc"} {
		if _, err := os.Stat(path); err == nil {
			args = append(args, "--ro-bind", path, path)
		}
	}
	args = append(args, "--bind", workspacePath, workspacePath, "--bind", cacheRoot, cacheRoot, "--tmpfs", "/tmp", "--chdir", workspacePath)
	args = append(args, argv...)
	return args, nil
}

func escapeSandboxPath(path string) string {
	path = strings.ReplaceAll(path, `\`, `\\`)
	return strings.ReplaceAll(path, `"`, `\"`)
}

func normalizeCommand(input CommandInput) ([]string, error) {
	if len(input.Args) > 0 {
		out := make([]string, 0, len(input.Args))
		for _, arg := range input.Args {
			if strings.TrimSpace(arg) != "" {
				out = append(out, arg)
			}
		}
		if len(out) == 0 {
			return nil, errors.New("command is required")
		}
		return out, nil
	}
	return splitCommandLine(input.Command)
}

func splitCommandLine(line string) ([]string, error) {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil, errors.New("command is required")
	}
	out := []string{}
	current := &strings.Builder{}
	quote := rune(0)
	escaped := false
	for _, r := range line {
		switch {
		case escaped:
			current.WriteRune(r)
			escaped = false
		case r == '\\':
			escaped = true
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				current.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote = r
		case r == ' ' || r == '\t':
			if current.Len() > 0 {
				out = append(out, current.String())
				current.Reset()
			}
		default:
			current.WriteRune(r)
		}
	}
	if quote != 0 {
		return nil, errors.New("unterminated quote")
	}
	if current.Len() > 0 {
		out = append(out, current.String())
	}
	return out, nil
}

type ownerCommandAssessment struct {
	Class                string
	RiskSummary          string
	RequiresConfirmation bool
	NeedsSandbox         bool
}

func assessOwnerCommand(argv []string) (ownerCommandAssessment, error) {
	if len(argv) == 0 {
		return ownerCommandAssessment{}, errors.New("command is required")
	}
	exe := strings.ToLower(filepath.Base(argv[0]))
	if exe == "." || exe == string(filepath.Separator) || filepath.IsAbs(argv[0]) || strings.Contains(argv[0], "/") || strings.Contains(argv[0], "\\") {
		return ownerCommandAssessment{}, errors.New("command executable must be a known tool name, not a path")
	}
	if isBlockedExecutable(exe) {
		return ownerCommandAssessment{}, errors.New("command is outside the controlled command surface")
	}
	if exe == "git" {
		return assessGitCommand(argv)
	}
	if exe == "go" {
		if len(argv) >= 2 {
			switch argv[1] {
			case "test", "vet":
				return ownerCommandAssessment{Class: "project-code", RiskSummary: "executes project Go code inside an OS sandbox with network disabled and writes limited to workspace/cache", RequiresConfirmation: true, NeedsSandbox: true}, nil
			}
		}
		return ownerCommandAssessment{}, errors.New("only go test and go vet are allowed")
	}
	if exe == "npm" || exe == "pnpm" || exe == "yarn" {
		return assessPackageScriptCommand(exe, argv)
	}
	return ownerCommandAssessment{}, errors.New("command is not allowlisted")
}

func assessGitCommand(argv []string) (ownerCommandAssessment, error) {
	if len(argv) < 2 {
		return ownerCommandAssessment{}, errors.New("git subcommand is required")
	}
	sub := strings.ToLower(argv[1])
	if err := validateGitInspectionArgs(argv); err != nil {
		return ownerCommandAssessment{}, err
	}
	switch sub {
	case "status", "diff", "log", "show", "branch", "rev-parse", "ls-files":
		return ownerCommandAssessment{Class: "git-read", RiskSummary: "read-only Git inspection in the registered workspace", RequiresConfirmation: false}, nil
	default:
		return ownerCommandAssessment{}, errors.New("only read-only git inspection commands are allowed")
	}
}

func validateGitInspectionArgs(argv []string) error {
	sub := ""
	if len(argv) > 1 {
		sub = strings.ToLower(argv[1])
	}
	for _, arg := range argv[1:] {
		lower := strings.ToLower(arg)
		if strings.HasPrefix(lower, "--exec-path") || strings.HasPrefix(lower, "-c") || strings.HasPrefix(lower, "--git-dir") || strings.HasPrefix(lower, "--work-tree") || strings.HasPrefix(lower, "--namespace") || strings.HasPrefix(lower, "--config-env") || strings.HasPrefix(lower, "--upload-pack") || strings.HasPrefix(lower, "--output") || lower == "--no-index" || lower == "--ext-diff" || lower == "--textconv" || strings.Contains(lower, "external") {
			return errors.New("git command option is not allowed")
		}
		if filepath.IsAbs(arg) || strings.HasPrefix(arg, "../") || strings.HasPrefix(arg, `..\`) || strings.Contains(arg, "/../") || strings.Contains(arg, `\..\`) {
			return errors.New("git command path must stay inside the registered workspace")
		}
	}
	if sub == "branch" {
		return validateGitBranchInspectionArgs(argv[2:])
	}
	return nil
}

func validateGitBranchInspectionArgs(args []string) error {
	for _, arg := range args {
		lower := strings.ToLower(arg)
		if strings.HasPrefix(lower, "-d") || strings.HasPrefix(lower, "-m") || strings.HasPrefix(lower, "--delete") || strings.HasPrefix(lower, "--move") || strings.HasPrefix(lower, "--copy") || strings.HasPrefix(lower, "--force") || strings.HasPrefix(lower, "--set-upstream") || strings.HasPrefix(lower, "--unset-upstream") || strings.HasPrefix(lower, "--edit-description") {
			return errors.New("git branch mutation option is not allowed")
		}
		if !strings.HasPrefix(arg, "-") {
			return errors.New("git branch positional arguments are not allowed")
		}
		if branchOptionRequiresValue(lower) {
			return errors.New("git branch options that require separate values are not allowed; use --option=value form")
		}
	}
	return nil
}

func branchOptionRequiresValue(arg string) bool {
	switch arg {
	case "--points-at", "--sort", "--format":
		return true
	default:
		return false
	}
}

func controlledGitArgv(argv []string) []string {
	if len(argv) < 2 || strings.ToLower(filepath.Base(argv[0])) != "git" {
		return argv
	}
	out := []string{"git", "-c", "core.pager=cat", "-c", "diff.external=", "-c", "pager.diff=false", "-c", "pager.show=false"}
	sub := strings.ToLower(argv[1])
	out = append(out, argv[1])
	if sub == "diff" || sub == "show" || sub == "log" {
		out = append(out, "--no-ext-diff", "--no-textconv")
	}
	out = append(out, argv[2:]...)
	return out
}

func assessPackageScriptCommand(exe string, argv []string) (ownerCommandAssessment, error) {
	script := ""
	if exe == "npm" {
		if len(argv) >= 2 && argv[1] == "test" {
			script = "test"
		} else if len(argv) >= 3 && argv[1] == "run" {
			script = argv[2]
		}
	} else if exe == "pnpm" || exe == "yarn" {
		if len(argv) >= 2 {
			script = argv[1]
		}
	}
	switch script {
	case "check", "typecheck", "lint", "test", "build":
		return ownerCommandAssessment{Class: "project-script", RiskSummary: "executes an allowlisted local project script inside an OS sandbox with network disabled and writes limited to workspace/cache", RequiresConfirmation: true, NeedsSandbox: true}, nil
	default:
		return ownerCommandAssessment{}, errors.New("only check, typecheck, lint, test and build package scripts are allowed")
	}
}

func isBlockedExecutable(exe string) bool {
	switch exe {
	case "sh", "bash", "zsh", "fish", "python", "python3", "node", "ruby", "perl", "php", "npx", "curl", "wget", "ssh", "scp", "sftp", "rsync", "nc", "netcat", "telnet", "docker", "kubectl", "sudo", "su", "rm", "mv", "cp", "chmod", "chown", "mkfs", "dd", "osascript", "open":
		return true
	default:
		return false
	}
}

func commandRiskLevel(assessment ownerCommandAssessment) string {
	if assessment.RequiresConfirmation {
		return "medium"
	}
	return "low"
}

func commandCompletionPreview(status string, exitCode int, errorSummary string) string {
	if errorSummary != "" {
		return Preview(status+": "+errorSummary, 300)
	}
	return Preview(status+" exit "+strconv.Itoa(exitCode), 120)
}

func (s *Service) commandCacheRoot(commandID string) string {
	return filepath.Join(s.dataDir, "command-cache", commandID)
}

func (s *Service) commandEnv(cacheRoot string) []string {
	env := BuildChildEnv("")
	_ = os.MkdirAll(cacheRoot, 0o700)
	pairs := []string{
		"CI=1",
		"NO_COLOR=1",
		"PAGER=cat",
		"GIT_PAGER=cat",
		"GIT_EXTERNAL_DIFF=",
		"GIT_OPTIONAL_LOCKS=0",
		"HOME=" + filepath.Join(cacheRoot, "home"),
		"XDG_CACHE_HOME=" + filepath.Join(cacheRoot, "xdg-cache"),
		"XDG_CONFIG_HOME=" + filepath.Join(cacheRoot, "xdg-config"),
		"TMPDIR=" + filepath.Join(cacheRoot, "tmp"),
		"GOCACHE=" + filepath.Join(cacheRoot, "go-build"),
		"GOMODCACHE=" + filepath.Join(cacheRoot, "go-mod"),
		"GOPROXY=off",
		"GOSUMDB=off",
		"GOTOOLCHAIN=local",
		"npm_config_cache=" + filepath.Join(cacheRoot, "npm"),
		"npm_config_offline=true",
	}
	for _, pair := range pairs {
		if strings.Contains(pair, "HOME=") || strings.Contains(pair, "XDG_CACHE_HOME=") || strings.Contains(pair, "XDG_CONFIG_HOME=") || strings.Contains(pair, "TMPDIR=") || strings.Contains(pair, "GOCACHE=") || strings.Contains(pair, "GOMODCACHE=") || strings.Contains(pair, "npm_config_cache=") {
			parts := strings.SplitN(pair, "=", 2)
			_ = os.MkdirAll(parts[1], 0o700)
		}
		env = append(env, pair)
	}
	return env
}

// ---- Preview browser ----

func (s *Service) ListBrowserSessions(ctx context.Context, threadID string) ([]storage.CodexCliBrowserSession, error) {
	return s.store.ListCodexCliBrowserSessions(ctx, threadID)
}

func (s *Service) CreateBrowserSession(ctx context.Context, threadID string, input BrowserSessionInput) (storage.CodexCliBrowserSession, error) {
	thread, ws, err := s.threadWorkspace(ctx, threadID)
	if err != nil {
		return storage.CodexCliBrowserSession{}, err
	}
	normalized, err := s.normalizePreviewURL(ctx, input.URL, ws, input.AllowPublic)
	if err != nil {
		return storage.CodexCliBrowserSession{}, err
	}
	session, err := s.store.CreateCodexCliBrowserSession(ctx, storage.CodexCliBrowserSession{ThreadID: thread.ID, WorkspaceID: ws.ID, URL: normalized, Status: "open"})
	if err != nil {
		return storage.CodexCliBrowserSession{}, err
	}
	s.appendThreadEvent(ctx, thread.ID, thread.LastTurnID, "browser.preview.opened", "owner", "browser", previewURLSummary(normalized), map[string]any{"sessionId": session.ID, "url": previewURLSummary(normalized)})
	return session, nil
}

func (s *Service) NavigateBrowserSession(ctx context.Context, id string, input BrowserSessionInput) (storage.CodexCliBrowserSession, error) {
	session, err := s.store.GetCodexCliBrowserSession(ctx, id)
	if err != nil {
		return storage.CodexCliBrowserSession{}, err
	}
	_, ws, err := s.threadWorkspace(ctx, session.ThreadID)
	if err != nil {
		return storage.CodexCliBrowserSession{}, err
	}
	normalized, err := s.normalizePreviewURL(ctx, input.URL, ws, input.AllowPublic)
	if err != nil {
		return storage.CodexCliBrowserSession{}, err
	}
	session.URL = normalized
	session.Status = "open"
	session.LastError = ""
	return s.store.UpdateCodexCliBrowserSession(ctx, session)
}

func (s *Service) GetBrowserSession(ctx context.Context, id string) (storage.CodexCliBrowserSession, error) {
	return s.store.GetCodexCliBrowserSession(ctx, id)
}

func (s *Service) DeleteBrowserSession(ctx context.Context, id string) error {
	return s.store.DeleteCodexCliBrowserSession(ctx, id)
}

func (s *Service) AddBrowserComment(ctx context.Context, id string, input BrowserCommentInput) error {
	session, err := s.store.GetCodexCliBrowserSession(ctx, id)
	if err != nil {
		return err
	}
	thread, err := s.store.GetCodexCliThread(ctx, session.ThreadID)
	if err != nil {
		return err
	}
	body := Preview(input.Body, 1000)
	if strings.TrimSpace(body) == "" {
		return errors.New("comment body is required")
	}
	s.appendThreadEvent(ctx, thread.ID, thread.LastTurnID, "browser.preview.comment", "owner", "browser", body, map[string]any{"sessionId": session.ID, "url": previewURLSummary(session.URL), "x": input.X, "y": input.Y})
	_, _ = s.store.AddAudit(ctx, storage.AuditEvent{EventType: "codex_cli.browser.comment_created", WorkspaceID: session.WorkspaceID, RiskLevel: "low", Summary: "已添加 Codex preview comment", Payload: map[string]any{"threadId": thread.ID, "sessionId": session.ID}})
	return nil
}

func (s *Service) normalizePreviewURL(ctx context.Context, raw string, ws storage.CodexCliWorkspace, allowPublic bool) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("url is required")
	}
	workspacePath := resolvedWorkspacePath(ws.Path)
	if strings.HasPrefix(raw, "file://") {
		u, err := url.Parse(raw)
		if err != nil {
			return "", err
		}
		path, err := filepath.EvalSymlinks(filepath.Clean(u.Path))
		if err != nil {
			return "", err
		}
		info, err := os.Stat(path)
		if err != nil {
			return "", err
		}
		if info.IsDir() {
			return "", errors.New("file preview must point to a file")
		}
		if !isInsidePath(workspacePath, path) {
			return "", errors.New("file preview must stay inside workspace")
		}
		u.Path = path
		return u.String(), nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", errors.New("only http, https and workspace file URLs are supported")
	}
	if u.User != nil {
		return "", errors.New("preview URL must not contain credentials")
	}
	if hasSensitiveQuery(u.RawQuery) {
		return "", errors.New("preview URL query contains sensitive-looking keys")
	}
	host := u.Hostname()
	if isLocalPreviewHost(host) {
		return u.String(), nil
	}
	if !allowPublic {
		return "", errors.New("public preview URL requires explicit owner allow")
	}
	if err := ensurePublicPreviewHost(ctx, host); err != nil {
		return "", err
	}
	return u.String(), nil
}

func (s *Service) FetchBrowserPreview(ctx context.Context, id string) (BrowserPreview, error) {
	session, err := s.store.GetCodexCliBrowserSession(ctx, id)
	if err != nil {
		return BrowserPreview{}, err
	}
	_, ws, err := s.threadWorkspace(ctx, session.ThreadID)
	if err != nil {
		return BrowserPreview{}, err
	}
	if _, err := s.normalizePreviewURL(ctx, session.URL, ws, true); err != nil {
		return BrowserPreview{}, err
	}
	proxyPath := browserProxyPath(session.ID)
	if strings.HasPrefix(session.URL, "file://") {
		u, err := url.Parse(session.URL)
		if err != nil {
			return BrowserPreview{}, err
		}
		data, err := readPreviewFile(u.Path, browserPreviewMaxBytes)
		if err != nil {
			return BrowserPreview{}, err
		}
		contentType := previewFileContentType(u.Path, "text/html; charset=utf-8")
		body := rewritePreviewBody(data, contentType, session.URL, proxyPath)
		return BrowserPreview{ContentType: contentType, Body: body, ScriptPolicy: "allowed"}, nil
	}
	cctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, session.URL, nil)
	if err != nil {
		return BrowserPreview{}, err
	}
	client := s.previewHTTPClient(ws)
	resp, err := client.Do(req)
	if err != nil {
		return BrowserPreview{}, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, browserPreviewMaxBytes))
	if err != nil {
		return BrowserPreview{}, err
	}
	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "text/html; charset=utf-8"
	}
	policy := "blocked"
	if previewURLIsLocal(session.URL) {
		policy = "allowed"
	}
	body := rewritePreviewBody(data, contentType, session.URL, proxyPath)
	return BrowserPreview{ContentType: contentType, Body: body, ScriptPolicy: policy}, nil
}

func (s *Service) FetchBrowserResource(ctx context.Context, id string, rawResourceURL string) (BrowserPreview, error) {
	session, err := s.store.GetCodexCliBrowserSession(ctx, id)
	if err != nil {
		return BrowserPreview{}, err
	}
	_, ws, err := s.threadWorkspace(ctx, session.ThreadID)
	if err != nil {
		return BrowserPreview{}, err
	}
	resourceURL, err := resolvePreviewResourceURL(session.URL, rawResourceURL)
	if err != nil {
		return BrowserPreview{}, err
	}
	normalized, err := s.normalizePreviewURL(ctx, resourceURL, ws, true)
	if err != nil {
		return BrowserPreview{}, err
	}
	if err := validatePreviewResourceTransition(session.URL, normalized); err != nil {
		return BrowserPreview{}, err
	}
	if strings.HasPrefix(normalized, "file://") {
		u, err := url.Parse(normalized)
		if err != nil {
			return BrowserPreview{}, err
		}
		data, err := readPreviewFile(u.Path, browserResourceMaxBytes)
		if err != nil {
			return BrowserPreview{}, err
		}
		contentType := previewFileContentType(u.Path, "application/octet-stream")
		return BrowserPreview{ContentType: contentType, Body: rewritePreviewBody(data, contentType, normalized, browserProxyPath(session.ID)), ScriptPolicy: "allowed"}, nil
	}
	cctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, normalized, nil)
	if err != nil {
		return BrowserPreview{}, err
	}
	resp, err := s.previewHTTPClient(ws).Do(req)
	if err != nil {
		return BrowserPreview{}, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, browserResourceMaxBytes))
	if err != nil {
		return BrowserPreview{}, err
	}
	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	policy := "blocked"
	if previewURLIsLocal(normalized) {
		policy = "allowed"
	}
	return BrowserPreview{ContentType: contentType, Body: rewritePreviewBody(data, contentType, normalized, browserProxyPath(session.ID)), ScriptPolicy: policy}, nil
}

func browserProxyPath(sessionID string) string {
	return "/api/codex/browser/sessions/" + url.PathEscape(sessionID) + "/proxy"
}

func rewritePreviewBody(data []byte, contentType string, rawBaseURL string, proxyPath string) []byte {
	lower := strings.ToLower(contentType)
	switch {
	case strings.Contains(lower, "html"):
		return []byte(rewritePreviewHTML(string(data), rawBaseURL, proxyPath))
	case strings.Contains(lower, "css"):
		baseURL, err := url.Parse(rawBaseURL)
		if err != nil {
			return data
		}
		return []byte(rewritePreviewCSS(string(data), baseURL, proxyPath))
	default:
		return data
	}
}

func rewritePreviewHTML(body string, rawBaseURL string, proxyPath string) string {
	if strings.TrimSpace(body) == "" {
		return body
	}
	baseURL, err := url.Parse(rawBaseURL)
	if err != nil {
		return body
	}
	doc, err := xhtml.Parse(strings.NewReader(body))
	if err != nil {
		return body
	}
	var walk func(*xhtml.Node)
	walk = func(node *xhtml.Node) {
		if node.Type == xhtml.ElementNode && strings.EqualFold(node.Data, "base") && node.Parent != nil {
			node.Parent.RemoveChild(node)
			return
		}
		if node.Type == xhtml.ElementNode {
			for i := range node.Attr {
				key := strings.ToLower(node.Attr[i].Key)
				switch key {
				case "src", "href", "action", "poster":
					if next, ok := proxiedPreviewResourceURL(baseURL, node.Attr[i].Val, proxyPath); ok {
						node.Attr[i].Val = next
					}
				case "srcset":
					node.Attr[i].Val = rewritePreviewSrcset(node.Attr[i].Val, baseURL, proxyPath)
				case "style":
					node.Attr[i].Val = rewritePreviewCSS(node.Attr[i].Val, baseURL, proxyPath)
				}
			}
		}
		if node.Type == xhtml.TextNode && node.Parent != nil && strings.EqualFold(node.Parent.Data, "style") {
			node.Data = rewritePreviewCSS(node.Data, baseURL, proxyPath)
		}
		for child := node.FirstChild; child != nil; {
			next := child.NextSibling
			walk(child)
			child = next
		}
	}
	walk(doc)
	var out bytes.Buffer
	if err := xhtml.Render(&out, doc); err != nil {
		return body
	}
	return out.String()
}

func rewritePreviewSrcset(value string, baseURL *url.URL, proxyPath string) string {
	parts := strings.Split(value, ",")
	for i, part := range parts {
		fields := strings.Fields(strings.TrimSpace(part))
		if len(fields) == 0 {
			continue
		}
		if next, ok := proxiedPreviewResourceURL(baseURL, fields[0], proxyPath); ok {
			fields[0] = next
			parts[i] = strings.Join(fields, " ")
		}
	}
	return strings.Join(parts, ", ")
}

func rewritePreviewCSS(css string, baseURL *url.URL, proxyPath string) string {
	return cssURLPattern.ReplaceAllStringFunc(css, func(match string) string {
		groups := cssURLPattern.FindStringSubmatch(match)
		if len(groups) < 3 {
			return match
		}
		quote := groups[1]
		if quote == "" {
			quote = `"`
		}
		if next, ok := proxiedPreviewResourceURL(baseURL, strings.TrimSpace(groups[2]), proxyPath); ok {
			return "url(" + quote + next + quote + ")"
		}
		return match
	})
}

func proxiedPreviewResourceURL(baseURL *url.URL, raw string, proxyPath string) (string, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return "", false
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return "", false
	}
	scheme := strings.ToLower(u.Scheme)
	switch scheme {
	case "data", "blob", "javascript", "mailto", "tel", "about":
		return "", false
	}
	absolute := baseURL.ResolveReference(u)
	if absolute == nil {
		return "", false
	}
	switch strings.ToLower(absolute.Scheme) {
	case "http", "https", "file":
		return proxyPath + "?url=" + url.QueryEscape(absolute.String()), true
	default:
		return "", false
	}
}

func resolvePreviewResourceURL(baseRaw string, resourceRaw string) (string, error) {
	if strings.TrimSpace(resourceRaw) == "" {
		return "", errors.New("resource url is required")
	}
	baseURL, err := url.Parse(baseRaw)
	if err != nil {
		return "", err
	}
	resourceURL, err := url.Parse(strings.TrimSpace(resourceRaw))
	if err != nil {
		return "", err
	}
	absolute := baseURL.ResolveReference(resourceURL)
	if absolute == nil {
		return "", errors.New("invalid resource url")
	}
	return absolute.String(), nil
}

func validatePreviewResourceTransition(sessionURL string, resourceURL string) error {
	if !previewURLIsPublic(sessionURL) {
		return nil
	}
	if previewURLIsPublic(resourceURL) {
		return nil
	}
	return errors.New("public preview resources must not target localhost, private network, or workspace file URLs")
}

func previewURLIsPublic(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	return !isLocalPreviewHost(u.Hostname())
}

func previewFileContentType(path string, fallback string) string {
	if value := mime.TypeByExtension(filepath.Ext(path)); value != "" {
		return value
	}
	return fallback
}

func readPreviewFile(path string, maxBytes int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return io.ReadAll(io.LimitReader(file, maxBytes))
}

func (s *Service) previewHTTPClient(ws storage.CodexCliWorkspace) *http.Client {
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			dialer := &net.Dialer{Timeout: 5 * time.Second}
			if isLocalPreviewHost(host) {
				return dialer.DialContext(ctx, network, address)
			}
			ips, err := resolvePreviewIPs(ctx, host)
			if err != nil {
				return nil, err
			}
			for _, ip := range ips {
				if disallowedPreviewIP(ip.IP) {
					return nil, errors.New("private network preview URL is not allowed")
				}
			}
			if len(ips) == 0 {
				return nil, errors.New("preview host did not resolve")
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
		},
	}
	return &http.Client{
		Transport: transport,
		Timeout:   8 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many preview redirects")
			}
			if len(via) > 0 && !previewURLIsLocal(via[0].URL.String()) && previewURLIsLocal(req.URL.String()) {
				return errors.New("public preview URL cannot redirect to local network")
			}
			_, err := s.normalizePreviewURL(req.Context(), req.URL.String(), ws, true)
			return err
		},
	}
}

func isLocalPreviewHost(host string) bool {
	host = strings.ToLower(strings.Trim(host, "[]"))
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

func previewURLIsLocal(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return u.Scheme == "file" || isLocalPreviewHost(u.Hostname())
}

func ensurePublicPreviewHost(ctx context.Context, host string) error {
	ips, err := resolvePreviewIPs(ctx, host)
	if err != nil {
		return err
	}
	if len(ips) == 0 {
		return errors.New("preview host did not resolve")
	}
	for _, ip := range ips {
		if disallowedPreviewIP(ip.IP) {
			return errors.New("private network preview URL is not allowed")
		}
	}
	return nil
}

func resolvePreviewIPs(ctx context.Context, host string) ([]net.IPAddr, error) {
	ip := net.ParseIP(host)
	if ip != nil {
		return []net.IPAddr{{IP: ip}}, nil
	}
	cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	ips, err := net.DefaultResolver.LookupIPAddr(cctx, host)
	if err != nil {
		return nil, errors.New("preview host did not resolve")
	}
	return ips, nil
}

func disallowedPreviewIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return true
	}
	return ip.Equal(net.ParseIP("169.254.169.254"))
}

func hasSensitiveQuery(rawQuery string) bool {
	if rawQuery == "" {
		return false
	}
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return true
	}
	for key := range values {
		lower := strings.ToLower(key)
		for _, marker := range []string{"token", "secret", "password", "passwd", "cookie", "authorization", "api_key", "apikey", "access_key"} {
			if strings.Contains(lower, marker) {
				return true
			}
		}
	}
	return false
}

func previewURLSummary(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return Preview(raw, 300)
	}
	u.RawQuery = ""
	u.Fragment = ""
	if u.User != nil {
		u.User = nil
	}
	return Preview(u.String(), 300)
}

func (s *Service) threadWorkspace(ctx context.Context, threadID string) (storage.CodexCliThread, storage.CodexCliWorkspace, error) {
	thread, err := s.store.GetCodexCliThread(ctx, threadID)
	if err != nil {
		return storage.CodexCliThread{}, storage.CodexCliWorkspace{}, err
	}
	ws, err := s.store.GetCodexCliWorkspace(ctx, thread.WorkspaceID)
	if err != nil {
		return storage.CodexCliThread{}, storage.CodexCliWorkspace{}, err
	}
	return thread, ws, nil
}
