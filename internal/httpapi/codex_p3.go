package httpapi

import (
	"errors"
	"net/http"

	"phantom-lancer/internal/codexclient"
)

func (s *Server) handleListCodexChats(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuth(w, r); !ok {
		return
	}
	includeArchived := r.URL.Query().Get("archived") == "1"
	query := r.URL.Query().Get("q")
	items, err := s.codex.ListChats(r.Context(), includeArchived, query)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleCreateCodexChat(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, ctx.Session) {
		return
	}
	var req codexclient.ChatThreadInput
	if !decodeJSON(w, r, &req) {
		return
	}
	thread, err := s.codex.CreateChat(r.Context(), req)
	if err != nil {
		if errors.Is(err, codexclient.ErrScratchWorkspaceUnset) {
			writeError(w, http.StatusBadRequest, "codex_scratch_unset", "请先在 Codex 设置中选择 scratch workspace")
			return
		}
		writeError(w, http.StatusBadRequest, "codex_chat_invalid", codexclient.Redact(err.Error(), 200))
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"thread": thread})
}

func (s *Server) handleCodexMemoryDiagnostics(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuth(w, r); !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"memory": s.codex.MemoryDiagnostics(r.Context())})
}
