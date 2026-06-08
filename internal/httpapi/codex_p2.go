package httpapi

import (
	"net/http"
	"strings"

	"phantom-lancer/internal/codexclient"
)

func (s *Server) handleListCodexAutomations(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuth(w, r); !ok {
		return
	}
	items, err := s.codex.ListAutomations(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleCreateCodexAutomation(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, ctx.Session) {
		return
	}
	var req codexclient.AutomationInput
	if !decodeJSON(w, r, &req) {
		return
	}
	automation, err := s.codex.CreateAutomation(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusBadRequest, "codex_automation_invalid", codexclient.Redact(err.Error(), 200))
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"automation": automation})
}

func (s *Server) handleCodexAutomationSubroutes(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, ctx.Session) {
		return
	}
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/codex/automations/"), "/")
	parts := strings.Split(path, "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, http.StatusNotFound, "not_found", "未找到 automation")
		return
	}
	id := parts[0]
	if r.Method == http.MethodDelete && len(parts) == 1 {
		if err := s.codex.DeleteAutomation(r.Context(), id); err != nil {
			writeError(w, http.StatusNotFound, "codex_automation_not_found", "未找到 automation")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
		return
	}
	if r.Method == http.MethodPatch && len(parts) == 1 {
		var req codexclient.AutomationPatch
		if !decodeJSON(w, r, &req) {
			return
		}
		automation, err := s.codex.UpdateAutomation(r.Context(), id, req)
		if err != nil {
			writeError(w, http.StatusBadRequest, "codex_automation_invalid", codexclient.Redact(err.Error(), 200))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"automation": automation})
		return
	}
	if r.Method == http.MethodPost && len(parts) == 2 && parts[1] == "run-now" {
		var req codexclient.RunAutomationInput
		if !decodeJSON(w, r, &req) {
			return
		}
		run, err := s.codex.RunAutomationNow(r.Context(), id, req)
		if err != nil {
			writeError(w, http.StatusBadRequest, "codex_automation_run_failed", codexclient.Redact(err.Error(), 200))
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"run": run})
		return
	}
	writeError(w, http.StatusNotFound, "not_found", "未找到 automation 路由")
}

func (s *Server) handleListCodexAutomationRuns(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuth(w, r); !ok {
		return
	}
	items, err := s.codex.ListAutomationRuns(r.Context(), r.URL.Query().Get("triage"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleCodexTriageInbox(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuth(w, r); !ok {
		return
	}
	inbox, err := s.codex.TriageInbox(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, inbox)
}

func (s *Server) handleCodexAutomationRunSubroutes(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, ctx.Session) {
		return
	}
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/codex/automation-runs/"), "/")
	parts := strings.Split(path, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] != "archive" {
		writeError(w, http.StatusNotFound, "not_found", "未找到 automation run 路由")
		return
	}
	run, err := s.codex.ArchiveAutomationRun(r.Context(), parts[0])
	if err != nil {
		writeError(w, http.StatusNotFound, "codex_automation_run_not_found", "未找到 automation run")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"run": run})
}

func (s *Server) handleCodexCapability(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuth(w, r); !ok {
		return
	}
	kind := strings.TrimPrefix(r.URL.Path, "/api/codex/capabilities/")
	summary, err := s.codex.CapabilitySummary(r.Context(), kind)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"capability": summary})
}

func (s *Server) handleProbeCodexCapabilities(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, ctx.Session) {
		return
	}
	if err := s.codex.ProbeCapabilities(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
