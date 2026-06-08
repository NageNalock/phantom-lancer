package httpapi

import (
	"net/http"
	"strings"

	"phantom-lancer/internal/codexclient"
)

func (s *Server) handleListNotifications(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuth(w, r); !ok {
		return
	}
	items, err := s.codex.ListNotifications(r.Context(), r.URL.Query().Get("scope"), r.URL.Query().Get("status"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleNotificationSubroutes(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, ctx.Session) {
		return
	}
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/notifications/"), "/")
	if id == "" {
		writeError(w, http.StatusNotFound, "not_found", "未找到通知")
		return
	}
	var req struct {
		Status string `json:"status"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	item, err := s.codex.UpdateNotificationStatus(r.Context(), id, req.Status)
	if err != nil {
		writeError(w, http.StatusBadRequest, "notification_update_failed", codexclient.Redact(err.Error(), 200))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"notification": item})
}

func (s *Server) handleArchiveReadNotifications(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, ctx.Session) {
		return
	}
	var req struct {
		Scope string `json:"scope"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	count, err := s.codex.ArchiveReadNotifications(r.Context(), req.Scope)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "notification_archive_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"archived": count})
}
