package codexclient

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"phantom-lancer/internal/storage"
)

type AutomationInput struct {
	Kind                  string         `json:"kind"`
	ThreadID              string         `json:"threadId"`
	WorkspaceID           string         `json:"workspaceId"`
	Title                 string         `json:"title"`
	Prompt                string         `json:"prompt"`
	Schedule              map[string]any `json:"schedule"`
	Enabled               bool           `json:"enabled"`
	DefaultSandbox        string         `json:"defaultSandbox"`
	DefaultApprovalPolicy string         `json:"defaultApprovalPolicy"`
}

type RunAutomationInput struct {
	ClientRequestID string `json:"clientRequestId"`
}

// AutomationPatch is a partial update payload: only fields present in the JSON
// body are applied, so callers can PATCH a single attribute (for example
// {"enabled": false}) without resetting the rest. Pointer fields distinguish
// "absent" from "zero value".
type AutomationPatch struct {
	Title                 *string         `json:"title"`
	Prompt                *string         `json:"prompt"`
	Schedule              *map[string]any `json:"schedule"`
	Enabled               *bool           `json:"enabled"`
	ThreadID              *string         `json:"threadId"`
	WorkspaceID           *string         `json:"workspaceId"`
	DefaultSandbox        *string         `json:"defaultSandbox"`
	DefaultApprovalPolicy *string         `json:"defaultApprovalPolicy"`
}

type CapabilitySummary struct {
	Kind      string           `json:"kind"`
	Status    string           `json:"status"`
	Items     []map[string]any `json:"items"`
	LastError string           `json:"lastError,omitempty"`
	ProbedAt  string           `json:"probedAt,omitempty"`
}

func (s *Service) ListAutomations(ctx context.Context) ([]storage.CodexCliAutomation, error) {
	return s.store.ListCodexCliAutomations(ctx)
}

func (s *Service) CreateAutomation(ctx context.Context, input AutomationInput) (storage.CodexCliAutomation, error) {
	automation, err := s.automationFromInput(ctx, storage.CodexCliAutomation{}, input)
	if err != nil {
		return storage.CodexCliAutomation{}, err
	}
	created, err := s.store.CreateCodexCliAutomation(ctx, automation)
	if err != nil {
		return storage.CodexCliAutomation{}, err
	}
	s.notify(ctx, storage.CodexCliNotification{Scope: "codex.automation", ScopeID: created.ID, EventType: "codex.automation.created", Title: "Automation created", Summary: created.Title, Severity: "neutral", Payload: map[string]any{"automationId": created.ID}})
	_, _ = s.store.AddAudit(ctx, storage.AuditEvent{EventType: "codex_cli.automation.created", WorkspaceID: created.WorkspaceID, RiskLevel: "low", Summary: "已创建 Codex automation", Payload: map[string]any{"automationId": created.ID, "kind": created.Kind}})
	return created, nil
}

func (s *Service) UpdateAutomation(ctx context.Context, id string, patch AutomationPatch) (storage.CodexCliAutomation, error) {
	current, err := s.store.GetCodexCliAutomation(ctx, id)
	if err != nil {
		return storage.CodexCliAutomation{}, err
	}
	// Seed the input from the current automation so unset patch fields keep their
	// existing values, then overlay only the fields present in the request.
	input := AutomationInput{
		Kind:                  current.Kind,
		ThreadID:              current.ThreadID,
		WorkspaceID:           current.WorkspaceID,
		Title:                 current.Title,
		Prompt:                current.PromptSummary,
		Schedule:              current.Schedule,
		Enabled:               current.Enabled,
		DefaultSandbox:        current.DefaultSandbox,
		DefaultApprovalPolicy: current.DefaultApprovalPolicy,
	}
	if patch.Title != nil {
		input.Title = *patch.Title
	}
	if patch.Prompt != nil {
		input.Prompt = *patch.Prompt
	}
	if patch.Schedule != nil {
		input.Schedule = *patch.Schedule
	}
	if patch.Enabled != nil {
		input.Enabled = *patch.Enabled
	}
	if patch.ThreadID != nil {
		input.ThreadID = *patch.ThreadID
	}
	if patch.WorkspaceID != nil {
		input.WorkspaceID = *patch.WorkspaceID
	}
	if patch.DefaultSandbox != nil {
		input.DefaultSandbox = *patch.DefaultSandbox
	}
	if patch.DefaultApprovalPolicy != nil {
		input.DefaultApprovalPolicy = *patch.DefaultApprovalPolicy
	}
	next, err := s.automationFromInput(ctx, current, input)
	if err != nil {
		return storage.CodexCliAutomation{}, err
	}
	next.ID = id
	updated, err := s.store.UpdateCodexCliAutomation(ctx, next)
	if err != nil {
		return storage.CodexCliAutomation{}, err
	}
	_, _ = s.store.AddAudit(ctx, storage.AuditEvent{EventType: "codex_cli.automation.updated", WorkspaceID: updated.WorkspaceID, RiskLevel: "low", Summary: "已更新 Codex automation", Payload: map[string]any{"automationId": updated.ID}})
	return updated, nil
}

func (s *Service) DeleteAutomation(ctx context.Context, id string) error {
	automation, err := s.store.GetCodexCliAutomation(ctx, id)
	if err != nil {
		return err
	}
	if err := s.store.DeleteCodexCliAutomation(ctx, id); err != nil {
		return err
	}
	_, _ = s.store.AddAudit(ctx, storage.AuditEvent{EventType: "codex_cli.automation.deleted", WorkspaceID: automation.WorkspaceID, RiskLevel: "low", Summary: "已删除 Codex automation", Payload: map[string]any{"automationId": id}})
	return nil
}

func (s *Service) RunAutomationNow(ctx context.Context, id string, input RunAutomationInput) (storage.CodexCliAutomationRun, error) {
	s.automationRunMu.Lock()
	defer s.automationRunMu.Unlock()

	automation, err := s.store.GetCodexCliAutomation(ctx, id)
	if err != nil {
		return storage.CodexCliAutomationRun{}, err
	}
	requestID := strings.TrimSpace(input.ClientRequestID)
	if requestID == "" {
		return storage.CodexCliAutomationRun{}, errors.New("client request id is required")
	}
	if existing, err := s.store.GetCodexCliAutomationRunByClientRequest(ctx, automation.ID, requestID); err == nil {
		return existing, nil
	}
	run, err := s.store.CreateCodexCliAutomationRun(ctx, storage.CodexCliAutomationRun{AutomationID: automation.ID, ClientRequestID: requestID, Status: "queued", TriageState: "open"})
	if err != nil {
		return storage.CodexCliAutomationRun{}, err
	}
	return s.executeAutomationRun(ctx, automation, run)
}

func (s *Service) ListAutomationRuns(ctx context.Context, triage string) ([]storage.CodexCliAutomationRun, error) {
	return s.store.ListCodexCliAutomationRuns(ctx, triage)
}

// TriageInbox aggregates the open work surfaces the design calls for: open
// automation runs, recent failed turns and unresolved review comments. It only
// returns summary fields; full prompts, diffs and outputs stay in their own
// detail APIs.
type TriageInbox struct {
	AutomationRuns    []storage.CodexCliAutomationRun `json:"automationRuns"`
	BackgroundThreads []storage.CodexCliThread        `json:"backgroundThreads"`
	FailedTurns       []TriageFailedTurn              `json:"failedTurns"`
	ReviewComments    []storage.CodexCliReviewComment `json:"reviewComments"`
}

type TriageFailedTurn struct {
	TurnID       string `json:"turnId"`
	ThreadID     string `json:"threadId"`
	ErrorSummary string `json:"errorSummary,omitempty"`
	CompletedAt  string `json:"completedAt,omitempty"`
}

func (s *Service) TriageInbox(ctx context.Context) (TriageInbox, error) {
	runs, err := s.store.ListCodexCliAutomationRuns(ctx, "open")
	if err != nil {
		return TriageInbox{}, err
	}
	failed, err := s.store.ListRecentFailedCodexCliTurns(ctx, 50)
	if err != nil {
		return TriageInbox{}, err
	}
	comments, err := s.store.ListOpenCodexCliReviewComments(ctx, 50)
	if err != nil {
		return TriageInbox{}, err
	}
	background, err := s.store.ListBackgroundCodexCliThreads(ctx, 50)
	if err != nil {
		return TriageInbox{}, err
	}
	failedTurns := make([]TriageFailedTurn, 0, len(failed))
	for _, turn := range failed {
		failedTurns = append(failedTurns, TriageFailedTurn{TurnID: turn.ID, ThreadID: turn.ThreadID, ErrorSummary: turn.ErrorSummary, CompletedAt: turn.CompletedAt})
	}
	return TriageInbox{AutomationRuns: runs, BackgroundThreads: background, FailedTurns: failedTurns, ReviewComments: comments}, nil
}

func (s *Service) ArchiveAutomationRun(ctx context.Context, id string) (storage.CodexCliAutomationRun, error) {
	return s.store.ArchiveCodexCliAutomationRun(ctx, id)
}

func (s *Service) processDueAutomations(ctx context.Context) {
	s.expireAutomationRuns(ctx)
	due, err := s.store.ListDueCodexCliAutomations(ctx, time.Now().UTC().Format(time.RFC3339Nano), 10)
	if err != nil {
		s.log.Warn("codex automation due query failed", "summary", Redact(err.Error(), 120))
		return
	}
	for _, automation := range due {
		requestID := "schedule-" + automation.NextRunAt
		run, err := s.store.CreateCodexCliAutomationRun(ctx, storage.CodexCliAutomationRun{AutomationID: automation.ID, ClientRequestID: requestID, Status: "queued", TriageState: "open"})
		if err != nil {
			continue
		}
		if run.TurnID != "" && (run.Status == "queued" || run.Status == "running") {
			continue
		}
		if _, err := s.executeAutomationRun(ctx, automation, run); err != nil {
			s.log.Warn("codex automation run failed", "summary", Redact(err.Error(), 120), "automation", automation.ID)
		}
	}
}

func (s *Service) executeAutomationRun(ctx context.Context, automation storage.CodexCliAutomation, run storage.CodexCliAutomationRun) (storage.CodexCliAutomationRun, error) {
	if run.TurnID != "" && (run.Status == "queued" || run.Status == "running") {
		return run, nil
	}
	if !s.currentSettings().Enabled {
		run.Status = "failed"
		run.ErrorSummary = "codex module disabled"
		run.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
		return s.finishAutomationRun(ctx, automation, run, false)
	}
	run.Status = "running"
	run.StartedAt = time.Now().UTC().Format(time.RFC3339Nano)
	run.LastHeartbeatAt = run.StartedAt
	run, _ = s.store.UpdateCodexCliAutomationRun(ctx, run)

	threadID := automation.ThreadID
	if automation.Kind == "project" {
		thread, err := s.CreateBackgroundThread(ctx, automation.WorkspaceID, automation.Title, "", "read-only", "on-request", "automation:"+automation.ID)
		if err != nil {
			run.Status = "failed"
			run.ErrorSummary = Redact(err.Error(), 200)
			run.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
			return s.finishAutomationRun(ctx, automation, run, false)
		}
		threadID = thread.ID
	}
	if threadID == "" {
		run.Status = "failed"
		run.ErrorSummary = "automation thread is required"
		run.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
		return s.finishAutomationRun(ctx, automation, run, false)
	}
	prompt := automation.PromptSummary
	if strings.TrimSpace(prompt) == "" {
		prompt = "Run a read-only diagnostic wakeup for this thread. Summarize findings and do not modify files."
	}
	turn, err := s.QueueTurn(ctx, threadID, TurnInput{Prompt: prompt, Sandbox: "read-only", ApprovalPolicy: "on-request"})
	if err != nil {
		run.Status = "failed"
		run.ErrorSummary = Redact(err.Error(), 200)
		run.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
		return s.finishAutomationRun(ctx, automation, run, false)
	}
	run.ThreadID = threadID
	run.TurnID = turn.ID
	run.Status = "running"
	run.FindingSummary = "Waiting for read-only Codex turn " + turn.ID
	run, err = s.store.UpdateCodexCliAutomationRun(ctx, run)
	if err != nil {
		return storage.CodexCliAutomationRun{}, err
	}
	next := automation
	next.LastRunAt = time.Now().UTC().Format(time.RFC3339Nano)
	next.NextRunAt = nextAutomationTime(next.Schedule, next.LastRunAt)
	_, _ = s.store.UpdateCodexCliAutomation(ctx, next)
	s.notify(ctx, storage.CodexCliNotification{Scope: "codex.automation", ScopeID: automation.ID, EventType: "codex.automation.run_queued", Title: "Automation queued", Summary: run.FindingSummary, Severity: "neutral", Payload: map[string]any{"automationId": automation.ID, "runId": run.ID, "threadId": threadID}})
	return run, nil
}

func (s *Service) completeAutomationRunForTurn(ctx context.Context, turn storage.CodexCliTurn, success bool, summary string) {
	run, err := s.store.GetCodexCliAutomationRunByTurn(ctx, turn.ID)
	if err != nil {
		return
	}
	automation, err := s.store.GetCodexCliAutomation(ctx, run.AutomationID)
	if err != nil {
		return
	}
	run.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if success {
		run.Status = "completed"
		run.FindingSummary = firstNonEmpty(s.automationFindingSummary(ctx, turn.ThreadID, turn.ID), summary, "Codex turn completed")
		_, _ = s.finishAutomationRun(ctx, automation, run, true)
		return
	}
	run.Status = "failed"
	run.ErrorSummary = firstNonEmpty(summary, "Codex turn failed")
	_, _ = s.finishAutomationRun(ctx, automation, run, false)
}

func (s *Service) automationFindingSummary(ctx context.Context, threadID string, turnID string) string {
	events, err := s.store.ListRecentCodexCliEventsForTurn(ctx, threadID, turnID, 200)
	if err != nil {
		return ""
	}
	for _, event := range events {
		switch event.EventType {
		case EventMessageAgent, EventDiagWarning, EventDiagError, EventPlanUpdated, EventDiffUpdated:
			if summary := strings.TrimSpace(event.TextPreview); summary != "" {
				return Preview(summary, 1000)
			}
		}
	}
	return ""
}

func (s *Service) finishAutomationRun(ctx context.Context, automation storage.CodexCliAutomation, run storage.CodexCliAutomationRun, success bool) (storage.CodexCliAutomationRun, error) {
	nowTime := time.Now().UTC().Format(time.RFC3339Nano)
	run.CompletedAt = firstNonEmpty(run.CompletedAt, nowTime)
	if success {
		automation.RetryCount = 0
		automation.FailureBackoffUntil = ""
		automation.NextRunAt = nextAutomationTime(automation.Schedule, nowTime)
	} else {
		automation.RetryCount++
		retryLimit := scheduleInt(automation.Schedule, "retryLimit", 3, 0, 10)
		if automation.RetryCount <= retryLimit {
			backoff := scheduleInt(automation.Schedule, "failureBackoffMinutes", 30, 5, 1440)
			automation.FailureBackoffUntil = time.Now().UTC().Add(time.Duration(backoff) * time.Minute).Format(time.RFC3339Nano)
			automation.NextRunAt = automation.FailureBackoffUntil
		} else {
			automation.RetryCount = 0
			automation.FailureBackoffUntil = ""
			automation.NextRunAt = nextAutomationTime(automation.Schedule, nowTime)
		}
	}
	automation.LastRunAt = firstNonEmpty(automation.LastRunAt, nowTime)
	_, _ = s.store.UpdateCodexCliAutomation(ctx, automation)
	updated, err := s.store.UpdateCodexCliAutomationRun(ctx, run)
	if err == nil {
		severity := "neutral"
		title := "Automation completed"
		summary := updated.FindingSummary
		if !success {
			severity = "danger"
			title = "Automation failed"
			summary = updated.ErrorSummary
		}
		s.notify(ctx, storage.CodexCliNotification{Scope: "codex.automation", ScopeID: automation.ID, EventType: "codex.automation.run_completed", Title: title, Summary: Preview(summary, 240), Severity: severity, Payload: map[string]any{"automationId": automation.ID, "runId": updated.ID, "threadId": updated.ThreadID, "turnId": updated.TurnID}})
	}
	return updated, err
}

func (s *Service) expireAutomationRuns(ctx context.Context) {
	active, err := s.store.ListActiveCodexCliAutomationRuns(ctx)
	if err != nil {
		return
	}
	nowTime := time.Now().UTC()
	for _, run := range active {
		automation, err := s.store.GetCodexCliAutomation(ctx, run.AutomationID)
		if err != nil {
			continue
		}
		started, err := time.Parse(time.RFC3339Nano, firstNonEmpty(run.StartedAt, run.CreatedAt))
		if err != nil {
			continue
		}
		maxMinutes := scheduleInt(automation.Schedule, "maxRuntimeMinutes", 60, 5, 24*60)
		if nowTime.Sub(started) < time.Duration(maxMinutes)*time.Minute {
			run.LastHeartbeatAt = nowTime.Format(time.RFC3339Nano)
			_, _ = s.store.UpdateCodexCliAutomationRun(ctx, run)
			continue
		}
		if run.TurnID != "" {
			_, _ = s.interruptTurn(ctx, run.TurnID, false, "automation run timed out")
		}
		run.Status = "failed"
		run.ErrorSummary = "automation run timed out"
		run.CompletedAt = nowTime.Format(time.RFC3339Nano)
		_, _ = s.finishAutomationRun(ctx, automation, run, false)
	}
}

func (s *Service) automationFromInput(ctx context.Context, current storage.CodexCliAutomation, input AutomationInput) (storage.CodexCliAutomation, error) {
	item := current
	item.Kind = input.Kind
	item.ThreadID = strings.TrimSpace(input.ThreadID)
	item.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	item.Title = strings.TrimSpace(input.Title)
	item.PromptSummary = Preview(input.Prompt, 1000)
	item.Schedule = input.Schedule
	item.Enabled = input.Enabled
	item.DefaultSandbox = "read-only"
	item.DefaultApprovalPolicy = "on-request"
	if item.Kind != "project" {
		item.Kind = "thread_wakeup"
	}
	if item.Kind == "thread_wakeup" {
		thread, err := s.store.GetCodexCliThread(ctx, item.ThreadID)
		if err != nil {
			return storage.CodexCliAutomation{}, err
		}
		item.WorkspaceID = thread.WorkspaceID
	} else if item.WorkspaceID == "" {
		return storage.CodexCliAutomation{}, errors.New("workspace id is required")
	}
	if item.Title == "" {
		item.Title = "Codex automation"
	}
	if item.Schedule == nil {
		item.Schedule = map[string]any{"intervalMinutes": 1440}
	}
	if expr := scheduleString(item.Schedule, "cron"); expr != "" {
		if err := validateCronExpr(expr); err != nil {
			return storage.CodexCliAutomation{}, errors.New("invalid cron expression: " + err.Error())
		}
	}
	// Recompute the next run from the (possibly updated) schedule so editing the
	// interval or cron takes effect immediately, while preserving a known next
	// run when neither schedule nor next run changed.
	scheduleChanged := !equalSchedule(current.Schedule, item.Schedule)
	if item.NextRunAt == "" || scheduleChanged {
		item.NextRunAt = nextAutomationTime(item.Schedule, time.Now().UTC().Format(time.RFC3339Nano))
	}
	return item, nil
}

func equalSchedule(a, b map[string]any) bool {
	left, errA := json.Marshal(a)
	right, errB := json.Marshal(b)
	if errA != nil || errB != nil {
		return false
	}
	return string(left) == string(right)
}

func nextAutomationTime(schedule map[string]any, from string) string {
	base, err := time.Parse(time.RFC3339Nano, from)
	if err != nil {
		base = time.Now().UTC()
	}
	if expr := scheduleString(schedule, "cron"); expr != "" {
		if next, ok := nextCronTime(expr, base); ok {
			return next.UTC().Format(time.RFC3339Nano)
		}
	}
	minutes := scheduleInt(schedule, "intervalMinutes", 1440, 15, 43200)
	return base.Add(time.Duration(minutes) * time.Minute).UTC().Format(time.RFC3339Nano)
}

func scheduleString(schedule map[string]any, key string) string {
	if raw, ok := schedule[key]; ok {
		if value, ok := raw.(string); ok {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func scheduleInt(schedule map[string]any, key string, fallback, minValue, maxValue int) int {
	value := fallback
	if raw, ok := schedule[key]; ok {
		switch v := raw.(type) {
		case float64:
			value = int(v)
		case int:
			value = v
		case string:
			if parsed, err := strconv.Atoi(v); err == nil {
				value = parsed
			}
		}
	}
	if value < minValue {
		value = minValue
	}
	if maxValue > 0 && value > maxValue {
		value = maxValue
	}
	return value
}

func (s *Service) ProbeCapabilities(ctx context.Context) error {
	client := s.supervisor.Client()
	for _, kind := range []string{"skills", "mcp", "plugins"} {
		item := storage.CodexCliCapabilityCache{
			ID:     kind,
			Kind:   kind,
			Status: "not_probeable",
			Payload: map[string]any{
				"items":   []any{},
				"message": "当前 Codex CLI 未提供稳定的安全摘要接口；Phantom Lancer 不解析含 secret 的完整配置。",
			},
		}
		if kind == "plugins" {
			item.Payload["message"] = "插件管理第一阶段只提供只读诊断；安装、升级、卸载不在 P2 实现。"
		}
		if s.detector.ResolveBinary() == "" && kind == "skills" {
			item.Status = "unavailable"
			item.LastError = "codex binary not found"
		}
		// Best-effort live probe via the managed app-server. Skills and MCP have
		// read-only list RPCs in newer app-server builds; when present we surface a
		// redacted summary, otherwise we keep the not_probeable placeholder. Plugins
		// stay read-only placeholder per P2 scope.
		if client != nil && kind != "plugins" {
			if items, ok := s.probeCapabilityItems(ctx, client, kind); ok {
				item.Status = "available"
				item.LastError = ""
				item.Payload = map[string]any{"items": items}
			}
		}
		if _, err := s.store.UpsertCodexCliCapabilityCache(ctx, item); err != nil {
			return err
		}
	}
	_, _ = s.store.AddAudit(ctx, storage.AuditEvent{EventType: "codex_cli.capabilities.probed", RiskLevel: "low", Summary: "已重新探测 Codex 扩展能力"})
	return nil
}

// probeCapabilityItems calls the app-server read-only list RPC for the given
// capability kind and returns redacted summary entries. The bool reports whether
// a usable response was obtained; transport errors or unsupported methods leave
// the caller on the not_probeable placeholder.
func (s *Service) probeCapabilityItems(ctx context.Context, client *AppServerClient, kind string) ([]any, bool) {
	var methods []string
	switch kind {
	case "skills":
		methods = []string{"skill/list", "skills/list"}
	case "mcp":
		methods = []string{"mcpServer/list", "mcp/list"}
	default:
		return nil, false
	}
	for _, method := range methods {
		cctx, cancel := context.WithTimeout(ctx, 8*time.Second)
		raw, err := client.Call(cctx, method, map[string]any{})
		cancel()
		if err != nil {
			continue
		}
		items := parseCapabilityList(raw, kind)
		return items, true
	}
	return nil, false
}

func (s *Service) CapabilitySummary(ctx context.Context, kind string) (CapabilitySummary, error) {
	item, err := s.store.GetCodexCliCapabilityCache(ctx, kind)
	if errors.Is(err, storage.ErrNotFound) {
		if err := s.ProbeCapabilities(ctx); err != nil {
			return CapabilitySummary{}, err
		}
		item, err = s.store.GetCodexCliCapabilityCache(ctx, kind)
	}
	if err != nil {
		return CapabilitySummary{}, err
	}
	items := []map[string]any{}
	if raw, ok := item.Payload["items"].([]any); ok {
		for _, entry := range raw {
			if mapped, ok := entry.(map[string]any); ok {
				items = append(items, mapped)
			}
		}
	}
	return CapabilitySummary{Kind: item.Kind, Status: item.Status, Items: items, LastError: item.LastError, ProbedAt: item.ProbedAt}, nil
}

func (s *Service) ListNotifications(ctx context.Context, scope, status string) ([]storage.CodexCliNotification, error) {
	return s.store.ListCodexCliNotifications(ctx, scope, status)
}

func (s *Service) UpdateNotificationStatus(ctx context.Context, id, status string) (storage.CodexCliNotification, error) {
	switch status {
	case "read", "archived", "unread":
	default:
		return storage.CodexCliNotification{}, errors.New("unsupported notification status")
	}
	return s.store.UpdateCodexCliNotificationStatus(ctx, id, status)
}

func (s *Service) ArchiveReadNotifications(ctx context.Context, scope string) (int64, error) {
	if strings.TrimSpace(scope) == "" {
		scope = "codex"
	}
	return s.store.ArchiveReadCodexCliNotifications(ctx, scope)
}

func (s *Service) notify(ctx context.Context, item storage.CodexCliNotification) {
	if strings.TrimSpace(item.Title) == "" {
		item.Title = item.EventType
	}
	created, err := s.store.CreateCodexCliNotification(ctx, item)
	if err != nil {
		s.log.Warn("codex notification create failed", "summary", Redact(err.Error(), 120))
		return
	}
	// Push a summary-only event so the notification center can update live without
	// polling. Detail still comes from the notifications API; SSE carries no
	// prompt, diff or stdout/stderr. A fixed scope id lets the UI subscribe once.
	if s.hub != nil {
		event, appendErr := s.store.AppendEvent(ctx, "codex.notifications", "default", "codex.notification.created", map[string]any{
			"notificationId": created.ID,
			"scope":          created.Scope,
			"scopeId":        created.ScopeID,
			"severity":       created.Severity,
		})
		if appendErr != nil {
			s.log.Warn("codex notification event append failed", "summary", Redact(appendErr.Error(), 120))
			return
		}
		s.hub.Publish(event)
	}
}

func (s *Service) notifyForThreadEvent(ctx context.Context, threadID, turnID, eventType, preview string) {
	switch eventType {
	case EventApprovalReq:
		s.notify(ctx, storage.CodexCliNotification{Scope: "codex.thread", ScopeID: threadID, EventType: eventType, Title: "Codex approval requested", Summary: Preview(preview, 240), Severity: "warn", Payload: map[string]any{"threadId": threadID, "turnId": turnID}})
	case EventTurnCompleted:
		s.notify(ctx, storage.CodexCliNotification{Scope: "codex.thread", ScopeID: threadID, EventType: eventType, Title: "Codex turn completed", Summary: "Turn completed", Severity: "neutral", Payload: map[string]any{"threadId": threadID, "turnId": turnID}})
	case EventTurnFailed:
		s.notify(ctx, storage.CodexCliNotification{Scope: "codex.thread", ScopeID: threadID, EventType: eventType, Title: "Codex turn failed", Summary: Preview(preview, 240), Severity: "danger", Payload: map[string]any{"threadId": threadID, "turnId": turnID}})
	case "command.owner.completed":
		severity := "neutral"
		title := "Command finished"
		lower := strings.ToLower(preview)
		if strings.Contains(lower, "failed") || strings.Contains(lower, "timeout") {
			severity = "danger"
			title = "Command failed"
		} else if strings.Contains(lower, "cancelled") {
			severity = "warn"
			title = "Command cancelled"
		}
		s.notify(ctx, storage.CodexCliNotification{Scope: "codex.thread", ScopeID: threadID, EventType: eventType, Title: title, Summary: Preview(preview, 240), Severity: severity, Payload: map[string]any{"threadId": threadID}})
	case "review.comment.created":
		s.notify(ctx, storage.CodexCliNotification{Scope: "codex.thread", ScopeID: threadID, EventType: eventType, Title: "Review comment", Summary: Preview(preview, 240), Severity: "neutral", Payload: map[string]any{"threadId": threadID, "turnId": turnID}})
	}
}
