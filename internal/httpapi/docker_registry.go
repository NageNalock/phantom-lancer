package httpapi

import (
	"net/http"
	"strings"

	"phantom-lancer/internal/safelog"
	"phantom-lancer/internal/storage"
)

func (s *Server) handleDockerRegistryStatus(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuth(w, r); !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": s.docker.RegistryStatus(r.Context())})
}

func (s *Server) handleDockerRegistrySettings(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	if r.Method == http.MethodGet {
		settings, err := s.docker.RegistrySettings(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "docker_registry_settings_invalid", safelog.Error(err, 200))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"settings": settings})
		return
	}
	if !s.requireCSRF(w, r, ctx.Session) {
		return
	}
	var req storage.DockerRegistrySettings
	if !decodeJSON(w, r, &req) {
		return
	}
	settings, err := s.docker.UpdateRegistrySettings(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusBadRequest, "docker_registry_settings_invalid", safelog.Error(err, 200))
		return
	}
	_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
		EventType: "docker.registry.settings.updated",
		RiskLevel: "high",
		Summary:   "已更新 Docker Registry 设置",
		Payload: map[string]any{
			"enabled":        settings.Enabled,
			"storageBackend": settings.StorageBackend,
			"objectPrefix":   safelog.Text(settings.ObjectPrefix, 120),
			"requireTls":     settings.RequireTLS,
		},
	})
	writeJSON(w, http.StatusOK, map[string]any{"settings": settings, "status": s.docker.RegistryStatus(r.Context())})
}

func (s *Server) handleDockerRegistryRepositories(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuth(w, r); !ok {
		return
	}
	items, err := s.docker.ListRegistryRepositories(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "docker_registry_failed", safelog.Error(err, 200))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleDockerRegistryRepositorySubroutes(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/docker/registry/repositories/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 2 {
		writeError(w, http.StatusNotFound, "not_found", "未找到仓库")
		return
	}
	index := -1
	for i, part := range parts {
		if part == "tags" || part == "manifests" {
			index = i
			break
		}
	}
	if index <= 0 || (parts[index] != "tags" && index+1 >= len(parts)) {
		writeError(w, http.StatusNotFound, "not_found", "未找到仓库子路由")
		return
	}
	repo := strings.Join(parts[:index], "/")
	switch parts[index] {
	case "tags":
		if r.Method == http.MethodGet {
			items, err := s.docker.ListRegistryTags(r.Context(), repo)
			if err != nil {
				writeError(w, http.StatusBadRequest, "docker_registry_failed", safelog.Error(err, 200))
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"items": items})
			return
		}
		if !s.requireCSRF(w, r, ctx.Session) {
			return
		}
		if index+1 >= len(parts) {
			writeError(w, http.StatusNotFound, "not_found", "未找到 tag")
			return
		}
		tag := parts[index+1]
		if err := s.docker.DeleteRegistryTag(r.Context(), repo, tag); err != nil {
			writeError(w, http.StatusBadRequest, "docker_registry_failed", safelog.Error(err, 200))
			return
		}
		s.auditDockerRegistry(r, "docker.registry.tag.deleted", "high", "已删除 Registry tag", repo, tag, nil)
		w.WriteHeader(http.StatusNoContent)
	case "manifests":
		digest := parts[index+1]
		if r.Method == http.MethodGet {
			manifest, err := s.docker.GetRegistryManifest(r.Context(), repo, digest)
			if err != nil {
				writeError(w, http.StatusBadRequest, "docker_registry_failed", safelog.Error(err, 200))
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"manifest": manifest})
			return
		}
		if !s.requireCSRF(w, r, ctx.Session) {
			return
		}
		if err := s.docker.DeleteRegistryManifest(r.Context(), repo, digest); err != nil {
			writeError(w, http.StatusBadRequest, "docker_registry_failed", safelog.Error(err, 200))
			return
		}
		s.auditDockerRegistry(r, "docker.registry.manifest.deleted", "high", "已删除 Registry manifest", repo, digest, nil)
		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, http.StatusNotFound, "not_found", "未找到仓库子路由")
	}
}

func (s *Server) handleDockerRegistryCredentials(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	if r.Method == http.MethodGet {
		items, err := s.docker.ListRegistryCredentials(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "docker_registry_failed", safelog.Error(err, 200))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
		return
	}
	if !s.requireCSRF(w, r, ctx.Session) {
		return
	}
	var req struct {
		Name             string   `json:"name"`
		Scopes           []string `json:"scopes"`
		RepositoryPrefix string   `json:"repositoryPrefix"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	result, err := s.docker.CreateRegistryCredential(r.Context(), req.Name, req.Scopes, req.RepositoryPrefix)
	if err != nil {
		writeError(w, http.StatusBadRequest, "docker_registry_failed", safelog.Error(err, 200))
		return
	}
	s.auditDockerRegistry(r, "docker.registry.credential.created", "medium", "已创建 Registry 凭据", result.Credential.ID, "", nil)
	writeJSON(w, http.StatusCreated, result)
}

func (s *Server) handleDockerRegistryCredentialSubroutes(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, ctx.Session) {
		return
	}
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/docker/registry/credentials/"), "/")
	parts := strings.Split(path, "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, http.StatusNotFound, "not_found", "未找到凭据")
		return
	}
	id := parts[0]
	if len(parts) == 2 && parts[1] == "rotate" {
		result, err := s.docker.RotateRegistryCredential(r.Context(), id)
		if err != nil {
			writeError(w, http.StatusBadRequest, "docker_registry_failed", safelog.Error(err, 200))
			return
		}
		s.auditDockerRegistry(r, "docker.registry.credential.rotated", "medium", "已轮换 Registry 凭据", id, "", nil)
		writeJSON(w, http.StatusOK, result)
		return
	}
	if r.Method == http.MethodDelete {
		if err := s.docker.DeleteRegistryCredential(r.Context(), id); err != nil {
			writeError(w, http.StatusBadRequest, "docker_registry_failed", safelog.Error(err, 200))
			return
		}
		s.auditDockerRegistry(r, "docker.registry.credential.deleted", "high", "已删除 Registry 凭据", id, "", nil)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var req storage.DockerRegistryCredential
	if !decodeJSON(w, r, &req) {
		return
	}
	req.ID = id
	updated, err := s.docker.UpdateRegistryCredential(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusBadRequest, "docker_registry_failed", safelog.Error(err, 200))
		return
	}
	s.auditDockerRegistry(r, "docker.registry.credential.updated", "medium", "已更新 Registry 凭据", id, "", nil)
	writeJSON(w, http.StatusOK, map[string]any{"credential": updated})
}

func (s *Server) handleDockerRegistryGC(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, ctx.Session) {
		return
	}
	result, err := s.docker.RegistryGCJob(r.Context())
	if err != nil {
		writeError(w, http.StatusBadRequest, "docker_registry_failed", safelog.Error(err, 200))
		return
	}
	s.auditDockerRegistry(r, "docker.registry.gc.requested", "critical", "已请求 Registry GC", result.Job.ID, "", nil)
	writeJSON(w, http.StatusAccepted, result)
}

func (s *Server) handleDockerRegistryNative(w http.ResponseWriter, r *http.Request) {
	s.docker.ServeRegistry(w, r)
}

func (s *Server) auditDockerRegistry(r *http.Request, event, risk, summary, target, detail string, err error) {
	payload := map[string]any{"target": safelog.Text(target, 160), "detail": safelog.Text(detail, 160)}
	if err != nil {
		payload["error"] = safelog.Error(err, 200)
	}
	_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{EventType: event, RiskLevel: risk, Summary: summary, Payload: payload})
}
