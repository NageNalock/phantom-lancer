package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"phantom-lancer/internal/codexclient"
	"phantom-lancer/internal/storage"
)

func (s *Server) handleCodexQueueStatus(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuth(w, r); !ok {
		return
	}
	status, err := s.codex.QueueStatus(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleCodexReviewSnapshot(w http.ResponseWriter, r *http.Request, threadID string) {
	snapshot, err := s.codex.ReviewSnapshot(r.Context(), threadID, r.URL.Query().Get("scope"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "codex_review_failed", codexclient.Redact(err.Error(), 200))
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func (s *Server) handleCreateCodexReviewComment(w http.ResponseWriter, r *http.Request, threadID string) {
	var req codexclient.ReviewCommentInput
	if !decodeJSON(w, r, &req) {
		return
	}
	comment, err := s.codex.CreateReviewComment(r.Context(), threadID, req)
	if err != nil {
		writeError(w, http.StatusBadRequest, "codex_review_comment_invalid", codexclient.Redact(err.Error(), 200))
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"comment": comment})
}

func (s *Server) handleCodexReviewCommentSubroutes(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, ctx.Session) {
		return
	}
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/codex/review/comments/"), "/")
	if id == "" {
		writeError(w, http.StatusNotFound, "not_found", "未找到 review comment")
		return
	}
	comment, err := s.codex.ResolveReviewComment(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "codex_review_comment_not_found", "未找到 review comment")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"comment": comment})
}

func (s *Server) handleListCodexCommands(w http.ResponseWriter, r *http.Request, threadID string) {
	items, err := s.codex.ListCommands(r.Context(), threadID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleCreateCodexCommand(w http.ResponseWriter, r *http.Request, threadID string) {
	var req codexclient.CommandInput
	if !decodeJSON(w, r, &req) {
		return
	}
	command, err := s.codex.RunCommand(r.Context(), threadID, req)
	if err != nil {
		writeError(w, http.StatusBadRequest, "codex_command_invalid", codexclient.Redact(err.Error(), 200))
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"command": command})
}

func (s *Server) handleAssessCodexCommand(w http.ResponseWriter, r *http.Request, threadID string) {
	var req codexclient.CommandInput
	if !decodeJSON(w, r, &req) {
		return
	}
	assessment, err := s.codex.AssessCommand(r.Context(), threadID, req)
	if err != nil {
		writeError(w, http.StatusBadRequest, "codex_command_invalid", codexclient.Redact(err.Error(), 200))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"assessment": assessment})
}

func (s *Server) handleCodexCommandSubroutes(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, ctx.Session) {
		return
	}
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/codex/commands/"), "/")
	parts := strings.Split(path, "/")
	if len(parts) != 2 || parts[0] == "" {
		writeError(w, http.StatusNotFound, "not_found", "未找到 command 路由")
		return
	}
	var (
		command storage.CodexCliCommand
		err     error
	)
	switch parts[1] {
	case "interrupt":
		command, err = s.codex.InterruptCommand(r.Context(), parts[0])
	case "attach":
		command, err = s.codex.AttachCommandOutput(r.Context(), parts[0])
	default:
		writeError(w, http.StatusNotFound, "not_found", "未找到 command 路由")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "codex_command_action_failed", codexclient.Redact(err.Error(), 200))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"command": command})
}

func (s *Server) handleListCodexBrowserSessions(w http.ResponseWriter, r *http.Request, threadID string) {
	items, err := s.codex.ListBrowserSessions(r.Context(), threadID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleCreateCodexBrowserSession(w http.ResponseWriter, r *http.Request, threadID string) {
	var req codexclient.BrowserSessionInput
	if !decodeJSON(w, r, &req) {
		return
	}
	session, err := s.codex.CreateBrowserSession(r.Context(), threadID, req)
	if err != nil {
		writeError(w, http.StatusBadRequest, "codex_browser_invalid", codexclient.Redact(err.Error(), 200))
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"session": session})
}

func (s *Server) handleCodexBrowserSessionSubroutes(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/codex/browser/sessions/"), "/")
	parts := strings.Split(path, "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, http.StatusNotFound, "not_found", "未找到 browser session")
		return
	}
	id := parts[0]
	if r.Method == http.MethodDelete && len(parts) == 1 {
		if !s.requireCSRF(w, r, ctx.Session) {
			return
		}
		if err := s.codex.DeleteBrowserSession(r.Context(), id); err != nil {
			writeError(w, http.StatusNotFound, "codex_browser_not_found", "未找到 browser session")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
		return
	}
	if len(parts) < 2 {
		writeError(w, http.StatusNotFound, "not_found", "未找到 browser session 路由")
		return
	}
	switch parts[1] {
	case "navigate":
		if !s.requireCSRF(w, r, ctx.Session) {
			return
		}
		var req codexclient.BrowserSessionInput
		if !decodeJSON(w, r, &req) {
			return
		}
		session, err := s.codex.NavigateBrowserSession(r.Context(), id, req)
		if err != nil {
			writeError(w, http.StatusBadRequest, "codex_browser_invalid", codexclient.Redact(err.Error(), 200))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"session": session})
	case "comments":
		if !s.requireCSRF(w, r, ctx.Session) {
			return
		}
		var req codexclient.BrowserCommentInput
		if !decodeJSON(w, r, &req) {
			return
		}
		if err := s.codex.AddBrowserComment(r.Context(), id, req); err != nil {
			writeError(w, http.StatusBadRequest, "codex_browser_comment_invalid", codexclient.Redact(err.Error(), 200))
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"ok": true})
	case "proxy":
		var (
			preview codexclient.BrowserPreview
			err     error
		)
		if resourceURL := r.URL.Query().Get("url"); resourceURL != "" {
			preview, err = s.codex.FetchBrowserResource(r.Context(), id, resourceURL)
		} else {
			preview, err = s.codex.FetchBrowserPreview(r.Context(), id)
		}
		if err != nil {
			status := http.StatusBadGateway
			if errors.Is(err, storage.ErrNotFound) {
				status = http.StatusNotFound
			}
			writeError(w, status, "codex_browser_proxy_failed", codexclient.Redact(err.Error(), 200))
			return
		}
		w.Header().Set("Content-Type", preview.ContentType)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		if preview.ScriptPolicy == "blocked" {
			w.Header().Set("Content-Security-Policy", "default-src 'none'; img-src 'self' data: blob: http: https:; style-src 'self' 'unsafe-inline' http: https:; font-src 'self' data: http: https:; form-action 'none'; base-uri 'none'")
		}
		_, _ = w.Write(preview.Body)
	default:
		writeError(w, http.StatusNotFound, "not_found", "未找到 browser session 路由")
	}
}
