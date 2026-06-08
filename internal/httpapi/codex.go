package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"phantom-lancer/internal/codexclient"
	"phantom-lancer/internal/storage"
)

func (s *Server) registerCodexRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/codex/status", s.handleCodexStatus)
	mux.HandleFunc("POST /api/codex/status/probe", s.handleCodexProbe)
	mux.HandleFunc("GET /api/codex/app-server/status", s.handleCodexAppServerStatus)
	mux.HandleFunc("POST /api/codex/app-server/start", s.handleCodexAppServerControl)
	mux.HandleFunc("POST /api/codex/app-server/stop", s.handleCodexAppServerControl)
	mux.HandleFunc("POST /api/codex/app-server/restart", s.handleCodexAppServerControl)
	mux.HandleFunc("GET /api/codex/settings", s.handleGetCodexSettings)
	mux.HandleFunc("PUT /api/codex/settings", s.handleUpdateCodexSettings)
	mux.HandleFunc("GET /api/codex/models", s.handleListCodexModels)

	mux.HandleFunc("GET /api/codex/workspaces", s.handleListCodexWorkspaces)
	mux.HandleFunc("POST /api/codex/workspaces", s.handleCreateCodexWorkspace)
	mux.HandleFunc("PATCH /api/codex/workspaces/", s.handleCodexWorkspaceSubroutes)
	mux.HandleFunc("DELETE /api/codex/workspaces/", s.handleCodexWorkspaceSubroutes)

	mux.HandleFunc("GET /api/codex/threads", s.handleListCodexThreads)
	mux.HandleFunc("POST /api/codex/threads", s.handleCreateCodexThread)
	mux.HandleFunc("GET /api/codex/threads/", s.handleCodexThreadSubroutes)
	mux.HandleFunc("POST /api/codex/threads/", s.handleCodexThreadSubroutes)
	mux.HandleFunc("PATCH /api/codex/threads/", s.handleCodexThreadSubroutes)

	mux.HandleFunc("POST /api/codex/turns/", s.handleCodexTurnSubroutes)
	mux.HandleFunc("GET /api/codex/runtime/queue", s.handleCodexQueueStatus)
	mux.HandleFunc("POST /api/codex/commands/", s.handleCodexCommandSubroutes)
	mux.HandleFunc("DELETE /api/codex/review/comments/", s.handleCodexReviewCommentSubroutes)
	mux.HandleFunc("POST /api/codex/browser/sessions/", s.handleCodexBrowserSessionSubroutes)
	mux.HandleFunc("GET /api/codex/browser/sessions/", s.handleCodexBrowserSessionSubroutes)
	mux.HandleFunc("DELETE /api/codex/browser/sessions/", s.handleCodexBrowserSessionSubroutes)
	mux.HandleFunc("GET /api/codex/automations", s.handleListCodexAutomations)
	mux.HandleFunc("POST /api/codex/automations", s.handleCreateCodexAutomation)
	mux.HandleFunc("PATCH /api/codex/automations/", s.handleCodexAutomationSubroutes)
	mux.HandleFunc("POST /api/codex/automations/", s.handleCodexAutomationSubroutes)
	mux.HandleFunc("DELETE /api/codex/automations/", s.handleCodexAutomationSubroutes)
	mux.HandleFunc("GET /api/codex/automation-runs", s.handleListCodexAutomationRuns)
	mux.HandleFunc("POST /api/codex/automation-runs/", s.handleCodexAutomationRunSubroutes)
	mux.HandleFunc("GET /api/codex/triage", s.handleCodexTriageInbox)
	mux.HandleFunc("GET /api/codex/chats", s.handleListCodexChats)
	mux.HandleFunc("POST /api/codex/chats", s.handleCreateCodexChat)
	mux.HandleFunc("GET /api/codex/memory", s.handleCodexMemoryDiagnostics)
	mux.HandleFunc("GET /api/codex/capabilities/skills", s.handleCodexCapability)
	mux.HandleFunc("GET /api/codex/capabilities/mcp", s.handleCodexCapability)
	mux.HandleFunc("GET /api/codex/capabilities/plugins", s.handleCodexCapability)
	mux.HandleFunc("POST /api/codex/capabilities/probe", s.handleProbeCodexCapabilities)

	mux.HandleFunc("GET /api/codex/approvals", s.handleListCodexApprovals)
	mux.HandleFunc("POST /api/codex/approvals/", s.handleCodexApprovalSubroutes)

	mux.HandleFunc("POST /api/codex/attachments", s.handleCreateCodexAttachment)
	mux.HandleFunc("DELETE /api/codex/attachments/", s.handleCodexAttachmentSubroutes)
}

func (s *Server) handleCodexStatus(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuth(w, r); !ok {
		return
	}
	status, err := s.codex.Status(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleCodexProbe(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, ctx.Session) {
		return
	}
	if err := s.codex.Probe(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	status, err := s.codex.Status(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleCodexAppServerStatus(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuth(w, r); !ok {
		return
	}
	writeJSON(w, http.StatusOK, s.codex.AppServerStatus())
}

func (s *Server) handleCodexAppServerControl(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, ctx.Session) {
		return
	}
	action := strings.TrimPrefix(r.URL.Path, "/api/codex/app-server/")
	var (
		status codexclient.AppServerStatus
		err    error
	)
	switch action {
	case "start":
		status, err = s.codex.StartAppServer(r.Context())
	case "stop":
		status = s.codex.StopAppServer(r.Context())
	case "restart":
		status, err = s.codex.RestartAppServer(r.Context())
	default:
		writeError(w, http.StatusNotFound, "not_found", "未找到 app-server 操作")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, "codex_app_server_failed", codexclient.Redact(err.Error(), 200))
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleGetCodexSettings(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuth(w, r); !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"settings": s.codex.Settings(r.Context())})
}

func (s *Server) handleListCodexModels(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuth(w, r); !ok {
		return
	}
	items, err := s.codex.ListModels(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleUpdateCodexSettings(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, ctx.Session) {
		return
	}
	var req codexclient.Settings
	if !decodeJSON(w, r, &req) {
		return
	}
	updated, err := s.codex.UpdateSettings(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusBadRequest, "codex_settings_invalid", err.Error())
		return
	}
	_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
		EventType: "codex_cli.settings.updated",
		RiskLevel: "low",
		Summary:   "已更新 Codex 模块设置",
		Payload: map[string]any{
			"enabled":          updated.Enabled,
			"appServerEnabled": updated.AppServerEnabled,
			"execFallback":     updated.ExecFallbackEnabled,
		},
	})
	writeJSON(w, http.StatusOK, map[string]any{"settings": updated})
}

// ---- workspaces ----

func (s *Server) handleListCodexWorkspaces(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuth(w, r); !ok {
		return
	}
	items, err := s.codex.ListWorkspaces(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleCreateCodexWorkspace(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, ctx.Session) {
		return
	}
	var req storage.CodexCliWorkspace
	if !decodeJSON(w, r, &req) {
		return
	}
	created, err := s.codex.CreateWorkspace(r.Context(), req)
	if err != nil {
		writeError(w, codexWorkspaceErrorStatus(err), codexWorkspaceErrorCode(err), err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"workspace": created})
}

func (s *Server) handleCodexWorkspaceSubroutes(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, ctx.Session) {
		return
	}
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/codex/workspaces/"), "/")
	if id == "" {
		writeError(w, http.StatusNotFound, "not_found", "未找到工作区")
		return
	}
	switch r.Method {
	case http.MethodPatch:
		var req storage.CodexCliWorkspace
		if !decodeJSON(w, r, &req) {
			return
		}
		req.ID = id
		updated, err := s.codex.UpdateWorkspace(r.Context(), req)
		if err != nil {
			writeError(w, codexWorkspaceErrorStatus(err), codexWorkspaceErrorCode(err), err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"workspace": updated})
	case http.MethodDelete:
		if err := s.codex.DeleteWorkspace(r.Context(), id); err != nil {
			writeError(w, http.StatusNotFound, "not_found", "未找到工作区")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "方法不允许")
	}
}

// ---- threads ----

func (s *Server) handleListCodexThreads(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuth(w, r); !ok {
		return
	}
	includeArchived := r.URL.Query().Get("archived") == "1"
	items, err := s.codex.ListThreadsFiltered(r.Context(), codexclient.ThreadListOptions{
		IncludeArchived: includeArchived,
		Query:           r.URL.Query().Get("q"),
		WorkspaceID:     firstQuery(r, "workspace_id", "workspaceId"),
		Status:          r.URL.Query().Get("status"),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleCreateCodexThread(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, ctx.Session) {
		return
	}
	var req struct {
		WorkspaceID    string `json:"workspaceId"`
		Title          string `json:"title"`
		Model          string `json:"model"`
		Sandbox        string `json:"sandbox"`
		ApprovalPolicy string `json:"approvalPolicy"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	thread, err := s.codex.CreateThread(r.Context(), req.WorkspaceID, req.Title, req.Model, req.Sandbox, req.ApprovalPolicy)
	if err != nil {
		writeError(w, http.StatusBadRequest, "codex_thread_invalid", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"thread": thread})
}

func (s *Server) handleCodexThreadSubroutes(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/codex/threads/"), "/")
	parts := strings.Split(path, "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, http.StatusNotFound, "not_found", "未找到会话")
		return
	}
	threadID := parts[0]

	// GET /threads/{id} and GET /threads/{id}/events
	if r.Method == http.MethodGet {
		if len(parts) == 1 {
			thread, err := s.codex.GetThread(r.Context(), threadID)
			if err != nil {
				writeError(w, http.StatusNotFound, "codex_thread_not_found", "未找到会话")
				return
			}
			turns, _ := s.codex.ListTurns(r.Context(), threadID)
			writeJSON(w, http.StatusOK, map[string]any{"thread": thread, "turns": turns})
			return
		}
		if len(parts) == 2 && parts[1] == "events" {
			s.handleCodexThreadEvents(w, r, threadID)
			return
		}
		if len(parts) == 2 && parts[1] == "review" {
			s.handleCodexReviewSnapshot(w, r, threadID)
			return
		}
		if len(parts) == 2 && parts[1] == "commands" {
			s.handleListCodexCommands(w, r, threadID)
			return
		}
		if len(parts) == 3 && parts[1] == "browser" && parts[2] == "sessions" {
			s.handleListCodexBrowserSessions(w, r, threadID)
			return
		}
		writeError(w, http.StatusNotFound, "not_found", "未找到会话路由")
		return
	}

	if !s.requireCSRF(w, r, ctx.Session) {
		return
	}

	if len(parts) == 3 {
		switch {
		case parts[1] == "review" && parts[2] == "comments":
			s.handleCreateCodexReviewComment(w, r, threadID)
			return
		case parts[1] == "browser" && parts[2] == "sessions":
			s.handleCreateCodexBrowserSession(w, r, threadID)
			return
		case parts[1] == "commands" && parts[2] == "assess":
			s.handleAssessCodexCommand(w, r, threadID)
			return
		}
	}

	if len(parts) == 2 {
		switch parts[1] {
		case "turns":
			s.handleCreateCodexTurn(w, r, threadID)
			return
		case "queue":
			s.handleQueueCodexTurn(w, r, threadID)
			return
		case "archive":
			thread, err := s.codex.ArchiveThread(r.Context(), threadID)
			if err != nil {
				writeError(w, http.StatusNotFound, "codex_thread_not_found", "未找到会话")
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"thread": thread})
			return
		case "resume":
			thread, err := s.codex.ResumeThread(r.Context(), threadID)
			if err != nil {
				writeError(w, http.StatusNotFound, "codex_thread_not_found", "未找到会话")
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"thread": thread})
			return
		case "fork":
			thread, err := s.codex.ForkThread(r.Context(), threadID)
			if err != nil {
				writeError(w, http.StatusBadRequest, "codex_thread_fork_failed", codexclient.Redact(err.Error(), 200))
				return
			}
			writeJSON(w, http.StatusCreated, map[string]any{"thread": thread})
			return
		case "commands":
			s.handleCreateCodexCommand(w, r, threadID)
			return
		}
	}

	if r.Method == http.MethodPatch && len(parts) == 1 {
		var req struct {
			Title      *string `json:"title"`
			Pinned     *bool   `json:"pinned"`
			Background *bool   `json:"background"`
		}
		if !decodeJSON(w, r, &req) {
			return
		}
		thread, err := s.codex.PatchThread(r.Context(), threadID, req.Title, req.Pinned, req.Background)
		if err != nil {
			writeError(w, http.StatusNotFound, "codex_thread_not_found", "未找到会话")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"thread": thread})
		return
	}
	writeError(w, http.StatusNotFound, "not_found", "未找到会话路由")
}

func (s *Server) handleCreateCodexTurn(w http.ResponseWriter, r *http.Request, threadID string) {
	s.handleStartOrQueueCodexTurn(w, r, threadID, false)
}

func (s *Server) handleQueueCodexTurn(w http.ResponseWriter, r *http.Request, threadID string) {
	s.handleStartOrQueueCodexTurn(w, r, threadID, true)
}

func (s *Server) handleStartOrQueueCodexTurn(w http.ResponseWriter, r *http.Request, threadID string, forceQueue bool) {
	var req struct {
		Prompt         string   `json:"prompt"`
		Sandbox        string   `json:"sandbox"`
		ApprovalPolicy string   `json:"approvalPolicy"`
		Model          string   `json:"model"`
		AttachmentIDs  []string `json:"attachmentIds"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Prompt) == "" {
		writeError(w, http.StatusBadRequest, "codex_prompt_required", "prompt 不能为空")
		return
	}
	input := codexclient.TurnInput{
		Prompt:         req.Prompt,
		Sandbox:        req.Sandbox,
		ApprovalPolicy: req.ApprovalPolicy,
		Model:          req.Model,
		AttachmentIDs:  req.AttachmentIDs,
	}
	var (
		turn storage.CodexCliTurn
		err  error
	)
	if forceQueue {
		turn, err = s.codex.QueueTurn(r.Context(), threadID, input)
	} else {
		turn, err = s.codex.StartTurn(r.Context(), threadID, input)
	}
	if err != nil {
		writeError(w, codexTurnErrorStatus(err), codexTurnErrorCode(err), codexclient.Redact(err.Error(), 200))
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"turn": turn})
}

func (s *Server) handleCodexThreadEvents(w http.ResponseWriter, r *http.Request, threadID string) {
	if _, err := s.codex.GetThread(r.Context(), threadID); err != nil {
		writeError(w, http.StatusNotFound, "codex_thread_not_found", "未找到会话")
		return
	}
	if r.URL.Query().Get("stream") == "1" {
		s.streamCodexThreadEvents(w, r, threadID)
		return
	}
	after := parseInt64(r.URL.Query().Get("after"))
	limit := parseInt(r.URL.Query().Get("limit"))
	items, err := s.codex.ListThreadEvents(r.Context(), threadID, after, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) streamCodexThreadEvents(w http.ResponseWriter, r *http.Request, threadID string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "stream_unsupported", "当前环境不支持事件流")
		return
	}
	after := parseInt64(r.Header.Get("Last-Event-ID"))
	if queryAfter := r.URL.Query().Get("after"); queryAfter != "" {
		after = parseInt64(queryAfter)
	}
	backlog, _ := s.codex.ListThreadEvents(r.Context(), threadID, after, 500)
	for _, event := range backlog {
		writeCodexSSE(w, event.Sequence, event.EventType, event)
	}
	flusher.Flush()

	ch := s.hub.Subscribe(r.Context(), "codex.thread", threadID)
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case event, ok := <-ch:
			if !ok {
				return
			}
			writeCodexSSE(w, event.Sequence, event.Type, event)
			flusher.Flush()
		case <-ticker.C:
			fmt.Fprint(w, ": heartbeat\n\n")
			flusher.Flush()
		}
	}
}

// ---- turns ----

func (s *Server) handleCodexTurnSubroutes(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, ctx.Session) {
		return
	}
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/codex/turns/"), "/")
	parts := strings.Split(path, "/")
	if len(parts) != 2 || parts[0] == "" {
		writeError(w, http.StatusNotFound, "not_found", "未找到 turn 路由")
		return
	}
	turnID := parts[0]
	switch parts[1] {
	case "interrupt", "cancel":
		turn, err := s.codex.InterruptTurn(r.Context(), turnID)
		if err != nil {
			writeError(w, http.StatusBadRequest, "codex_turn_interrupt_failed", codexclient.Redact(err.Error(), 200))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"turn": turn})
	case "steer":
		var req struct {
			Prompt string `json:"prompt"`
		}
		if !decodeJSON(w, r, &req) {
			return
		}
		if err := s.codex.SteerTurn(r.Context(), turnID, req.Prompt); err != nil {
			writeError(w, http.StatusBadRequest, "codex_turn_steer_failed", codexclient.Redact(err.Error(), 200))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		writeError(w, http.StatusNotFound, "not_found", "未找到 turn 路由")
	}
}

// ---- approvals ----

func (s *Server) handleListCodexApprovals(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuth(w, r); !ok {
		return
	}
	status := r.URL.Query().Get("status")
	if status == "" {
		status = "pending"
	}
	items, err := s.codex.ListApprovals(r.Context(), status, r.URL.Query().Get("threadId"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleCodexApprovalSubroutes(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, ctx.Session) {
		return
	}
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/codex/approvals/"), "/")
	parts := strings.Split(path, "/")
	if len(parts) != 2 || parts[0] == "" {
		writeError(w, http.StatusNotFound, "not_found", "未找到审批路由")
		return
	}
	id := parts[0]
	decision := "decline"
	switch parts[1] {
	case "approve":
		decision = "accept"
	case "deny":
		decision = "decline"
	case "cancel":
		decision = "cancel"
	default:
		writeError(w, http.StatusNotFound, "not_found", "未找到审批路由")
		return
	}
	resolved, err := s.codex.ResolveApprovalDecision(r.Context(), id, decision)
	if err != nil {
		if errors.Is(err, codexclient.ErrApprovalNotLive) {
			writeError(w, http.StatusConflict, "codex_approval_stale", "审批请求已失效")
			return
		}
		writeError(w, http.StatusNotFound, "codex_approval_not_found", "未找到审批")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"approval": resolved})
}

// ---- attachments ----

func (s *Server) handleCreateCodexAttachment(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, ctx.Session) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, codexclient.MaxAttachmentBytes+1024)
	if err := r.ParseMultipartForm(codexclient.MaxAttachmentBytes); err != nil {
		writeError(w, http.StatusBadRequest, "codex_attachment_invalid", "附件无效或过大")
		return
	}
	threadID := r.FormValue("threadId")
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "codex_attachment_invalid", "缺少附件文件")
		return
	}
	defer file.Close()
	data := make([]byte, 0, header.Size)
	buf := make([]byte, 32*1024)
	for {
		n, readErr := file.Read(buf)
		if n > 0 {
			data = append(data, buf[:n]...)
			if len(data) > codexclient.MaxAttachmentBytes {
				writeError(w, http.StatusBadRequest, "codex_attachment_invalid", "附件过大")
				return
			}
		}
		if readErr != nil {
			break
		}
	}
	contentType := header.Header.Get("Content-Type")
	att, err := s.codex.CreateAttachment(r.Context(), threadID, header.Filename, contentType, data)
	if err != nil {
		writeError(w, http.StatusBadRequest, "codex_attachment_invalid", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"attachment": att})
}

func (s *Server) handleCodexAttachmentSubroutes(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, ctx.Session) {
		return
	}
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/codex/attachments/"), "/")
	if id == "" {
		writeError(w, http.StatusNotFound, "not_found", "未找到附件")
		return
	}
	if err := s.codex.DeleteAttachment(r.Context(), id); err != nil {
		writeError(w, http.StatusNotFound, "not_found", "未找到附件")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

// ---- helpers ----

func writeCodexSSE(w http.ResponseWriter, sequence int64, eventType string, payload any) {
	data, _ := json.Marshal(payload)
	fmt.Fprintf(w, "id: %d\n", sequence)
	fmt.Fprintf(w, "event: %s\n", eventType)
	fmt.Fprintf(w, "data: %s\n\n", data)
}

func codexWorkspaceErrorStatus(err error) int {
	switch {
	case errors.Is(err, codexclient.ErrPathOutOfBoundary), errors.Is(err, codexclient.ErrPathNotFound), errors.Is(err, codexclient.ErrPathNotDirectory):
		return http.StatusBadRequest
	default:
		return http.StatusBadRequest
	}
}

func codexWorkspaceErrorCode(err error) string {
	switch {
	case errors.Is(err, codexclient.ErrPathOutOfBoundary):
		return "path_out_of_boundary"
	case errors.Is(err, codexclient.ErrPathNotFound):
		return "workspace_path_missing"
	case errors.Is(err, codexclient.ErrPathNotDirectory):
		return "workspace_path_invalid"
	default:
		return "codex_workspace_invalid"
	}
}

func codexTurnErrorStatus(err error) int {
	switch {
	case errors.Is(err, codexclient.ErrPolicyViolation):
		return http.StatusForbidden
	case errors.Is(err, codexclient.ErrModuleDisabled):
		return http.StatusServiceUnavailable
	case errors.Is(err, codexclient.ErrNoRunner):
		return http.StatusServiceUnavailable
	case errors.Is(err, codexclient.ErrTurnInProgress):
		return http.StatusConflict
	default:
		return http.StatusBadRequest
	}
}

func codexTurnErrorCode(err error) string {
	switch {
	case errors.Is(err, codexclient.ErrPolicyViolation):
		return "permission_denied"
	case errors.Is(err, codexclient.ErrModuleDisabled):
		return "codex_disabled"
	case errors.Is(err, codexclient.ErrNoRunner):
		return "codex_no_runner"
	case errors.Is(err, codexclient.ErrTurnInProgress):
		return "codex_turn_in_progress"
	default:
		return "codex_turn_failed"
	}
}
