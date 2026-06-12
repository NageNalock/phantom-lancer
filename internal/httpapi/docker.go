package httpapi

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"phantom-lancer/internal/dockercontrol"
	"phantom-lancer/internal/safelog"
	"phantom-lancer/internal/storage"
)

// Docker Host read-only API (design P2). All endpoints require auth. Daemon
// unavailability is reported through the status endpoint rather than as a hard
// error so the UI can render an actionable empty state. No write or destructive
// operations are exposed in this phase.

func (s *Server) handleDockerStatus(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuth(w, r); !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": s.docker.Status(r.Context()), "control": s.docker.ControlStatus(r.Context())})
}

func (s *Server) handleDockerOverview(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuth(w, r); !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"host":     s.docker.Status(r.Context()),
		"control":  s.docker.ControlStatus(r.Context()),
		"registry": s.docker.RegistryStatus(r.Context()),
	})
}

func (s *Server) handleDockerProbe(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, ctx.Session) {
		return
	}
	status := s.docker.Status(r.Context())
	_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
		EventType: "docker.host.probed",
		RiskLevel: "low",
		Summary:   "已探测 Docker Host",
		Payload:   map[string]any{"state": status.State, "available": status.Available, "version": safelog.Text(status.ServerVersion, 80)},
	})
	writeJSON(w, http.StatusOK, map[string]any{"status": status})
}

func (s *Server) handleDockerHostEvents(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuth(w, r); !ok {
		return
	}
	scopeID := r.URL.Query().Get("id")
	limit := parseInt(r.URL.Query().Get("limit"))
	var (
		items any
		err   error
	)
	if scopeID == "" {
		if limit <= 0 || limit > 500 {
			limit = 200
		}
		items, err = s.store.ListRecentEventsByScope(r.Context(), "docker.job", limit)
	} else {
		if limit <= 0 || limit > 1000 {
			limit = 200
		}
		items, err = s.store.ListEvents(r.Context(), "docker.job", scopeID, parseInt64(r.URL.Query().Get("after")), limit)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", safelog.Error(err, 200))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleDockerJobs(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/docker/jobs"), "/")
	if path == "" && r.Method == http.MethodGet {
		limit := parseInt(r.URL.Query().Get("limit"))
		writeJSON(w, http.StatusOK, map[string]any{"items": s.docker.ListJobs(limit)})
		return
	}
	parts := strings.Split(path, "/")
	if len(parts) == 2 && parts[1] == "cancel" && r.Method == http.MethodPost {
		if !s.requireCSRF(w, r, ctx.Session) {
			return
		}
		job, err := s.docker.CancelJob(r.Context(), parts[0])
		if err != nil {
			writeError(w, http.StatusBadRequest, "docker_job_cancel_failed", err.Error())
			return
		}
		risk := job.RiskLevel
		if risk == "" {
			risk = "medium"
		}
		_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
			EventType: "docker.job.cancel.requested",
			RiskLevel: risk,
			Summary:   "已请求取消 Docker job",
			Payload:   map[string]any{"job": safelog.Text(job.ID, 80), "type": safelog.Text(job.Type, 120), "target": safelog.Text(job.Target, 120)},
		})
		writeJSON(w, http.StatusOK, map[string]any{"job": job})
		return
	}
	writeError(w, http.StatusNotFound, "not_found", "未找到 Docker job")
}

func (s *Server) handleDockerControlStatus(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuth(w, r); !ok {
		return
	}
	writeJSON(w, http.StatusOK, s.docker.ControlStatus(r.Context()))
}

func (s *Server) handleDockerUpdateSettings(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, ctx.Session) {
		return
	}
	var req struct {
		InstallEnabled         bool `json:"installEnabled"`
		DaemonControlEnabled   bool `json:"daemonControlEnabled"`
		ContainerCreateEnabled bool `json:"containerCreateEnabled"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	settings, err := s.docker.UpdateSettings(r.Context(), dockercontrol.Settings{
		InstallEnabled:         req.InstallEnabled,
		DaemonControlEnabled:   req.DaemonControlEnabled,
		ContainerCreateEnabled: req.ContainerCreateEnabled,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "docker_settings_invalid", err.Error())
		return
	}
	_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
		EventType: "docker.settings.updated",
		RiskLevel: "high",
		Summary:   "已更新 Docker 高权限开关",
		Payload: map[string]any{
			"installEnabled":         settings.InstallEnabled,
			"daemonControlEnabled":   settings.DaemonControlEnabled,
			"containerCreateEnabled": settings.ContainerCreateEnabled,
		},
	})
	writeJSON(w, http.StatusOK, map[string]any{"settings": settings, "control": s.docker.ControlStatus(r.Context())})
}

func (s *Server) handleDockerInstall(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, ctx.Session) {
		return
	}
	result, err := s.docker.InstallDockerJob(r.Context())
	if err != nil {
		s.auditDockerHost(r, "docker.install.requested", "critical", "Docker daemon 安装未启动", "install", false, err)
		writeError(w, http.StatusBadRequest, "docker_install_unavailable", err.Error())
		return
	}
	s.auditDockerHost(r, "docker.install.requested", "critical", "已请求安装 Docker daemon", result.Job.ID, true, nil)
	writeJSON(w, http.StatusAccepted, result)
}

func (s *Server) handleDockerDaemonControl(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, ctx.Session) {
		return
	}
	action := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/docker/daemon/"), "/")
	result, err := s.docker.DaemonControlJob(r.Context(), action)
	if err != nil {
		s.auditDockerHost(r, "docker.daemon."+action+".requested", "critical", "Docker daemon 控制未启动", action, false, err)
		writeError(w, http.StatusBadRequest, "docker_daemon_control_unavailable", err.Error())
		return
	}
	s.auditDockerHost(r, "docker.daemon."+action+".requested", "critical", "已请求 Docker daemon "+action, result.Job.ID, true, nil)
	writeJSON(w, http.StatusAccepted, result)
}

func (s *Server) handleDockerListContainers(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuth(w, r); !ok {
		return
	}
	items, err := s.docker.ListContainers(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, "docker_unavailable", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleDockerCreateContainer(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, ctx.Session) {
		return
	}
	if !s.docker.Settings(r.Context()).ContainerCreateEnabled {
		writeError(w, http.StatusForbidden, "docker_container_create_disabled", "模板化容器创建未开启")
		return
	}
	settings, err := s.docker.RegistrySettings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "docker_registry_settings_invalid", err.Error())
		return
	}
	var req dockercontrol.CreateContainerRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if settings.PublicURL != "" {
		parsed, _ := url.Parse(settings.PublicURL)
		allowed := strings.Trim(parsed.Host+"/personal", "/")
		if !strings.HasPrefix(strings.TrimSpace(req.Image), allowed+"/") {
			writeError(w, http.StatusForbidden, "docker_container_image_denied", "镜像不在允许的 Registry prefix 内")
			return
		}
	}
	result, err := s.docker.StartJob(r.Context(), "docker.container.create", "创建并启动容器", "critical", dockerShortID(req.Image), map[string]any{"image": safelog.Text(req.Image, 120), "name": safelog.Text(req.Name, 80)}, func(ctx context.Context, emit func(string, map[string]any)) error {
		emit("docker.job.output", map[string]any{"stream": "stdout", "message": "开始创建受限容器"})
		id, err := s.docker.CreateAndStartContainer(ctx, req)
		if id != "" {
			emit("docker.job.output", map[string]any{"stream": "stdout", "message": "容器已创建：" + id})
		}
		return err
	})
	if err != nil {
		s.auditDockerHost(r, "docker.container.create.requested", "critical", "容器创建任务未启动", req.Image, false, err)
		writeError(w, http.StatusBadGateway, "docker_unavailable", err.Error())
		return
	}
	s.auditDockerHost(r, "docker.container.create.requested", "critical", "已请求创建容器", req.Image, true, nil)
	writeJSON(w, http.StatusAccepted, result)
}

func (s *Server) handleDockerListImages(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuth(w, r); !ok {
		return
	}
	items, err := s.docker.ListImages(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, "docker_unavailable", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleDockerListVolumes(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuth(w, r); !ok {
		return
	}
	items, err := s.docker.ListVolumes(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, "docker_unavailable", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleDockerListNetworks(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuth(w, r); !ok {
		return
	}
	items, err := s.docker.ListNetworks(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, "docker_unavailable", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// ---- controlled operations (design P3) ----
//
// All mutating routes require CSRF, write an audit entry with the design's risk
// level, and surface daemon errors as docker_unavailable. Container kill/remove
// and image remove are destructive and the frontend must confirm them.

func (s *Server) handleDockerContainerSubroutes(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/docker/containers/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, http.StatusNotFound, "not_found", "未找到容器")
		return
	}
	id := parts[0]

	// Read-only subroutes: logs and stats (GET, no CSRF).
	if len(parts) == 2 && r.Method == http.MethodGet {
		switch parts[1] {
		case "inspect":
			summary, err := s.docker.ContainerInspectSummary(r.Context(), id)
			if err != nil {
				writeError(w, http.StatusBadGateway, "docker_unavailable", err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"container": summary})
		case "logs":
			tail := 0
			if raw := strings.TrimSpace(r.URL.Query().Get("tail")); raw != "" {
				if parsed, err := strconv.Atoi(raw); err == nil {
					tail = parsed
				}
			}
			lines, err := s.docker.ContainerLogs(r.Context(), id, tail)
			if err != nil {
				writeError(w, http.StatusBadGateway, "docker_unavailable", err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"lines": lines})
		case "stats":
			stats, err := s.docker.ContainerStats(r.Context(), id)
			if err != nil {
				writeError(w, http.StatusBadGateway, "docker_unavailable", err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"stats": stats})
		default:
			writeError(w, http.StatusNotFound, "not_found", "未找到容器子路由")
		}
		return
	}

	// Mutating subroutes require CSRF.
	if !s.requireCSRF(w, r, ctx.Session) {
		return
	}

	// DELETE /api/docker/containers/{id} -> remove (destructive).
	if len(parts) == 1 && r.Method == http.MethodDelete {
		force := strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("force")), "true")
		result, err := s.docker.StartJob(r.Context(), "docker.container.remove", "删除容器", "high", dockerShortID(id), map[string]any{"container": dockerShortID(id), "force": force}, func(ctx context.Context, emit func(string, map[string]any)) error {
			emit("docker.job.output", map[string]any{"stream": "stdout", "message": "开始删除容器"})
			return s.docker.RemoveContainer(ctx, id, force)
		})
		if err != nil {
			s.auditDocker(r, "docker.container.remove.requested", "high", "删除容器任务创建失败", id, false, err)
			writeError(w, http.StatusBadGateway, "docker_unavailable", err.Error())
			return
		}
		s.auditDocker(r, "docker.container.remove.requested", "high", "已请求删除容器", id, true, nil)
		writeJSON(w, http.StatusAccepted, result)
		return
	}

	if len(parts) != 2 || r.Method != http.MethodPost {
		writeError(w, http.StatusNotFound, "not_found", "未找到容器子路由")
		return
	}

	switch parts[1] {
	case "start":
		s.runContainerAction(w, r, id, "start")
	case "stop":
		s.runContainerAction(w, r, id, "stop")
	case "restart":
		s.runContainerAction(w, r, id, "restart")
	case "kill":
		s.runContainerAction(w, r, id, "kill")
	default:
		writeError(w, http.StatusNotFound, "not_found", "未找到容器子路由")
	}
}

func (s *Server) runContainerAction(w http.ResponseWriter, r *http.Request, id, action string) {
	var (
		run     func(context.Context, string) error
		event   string
		risk    string
		okText  string
		errText string
	)
	switch action {
	case "start":
		run, event, risk, okText, errText = s.docker.StartContainer, "docker.container.start.requested", "medium", "已请求启动容器", "启动容器任务创建失败"
	case "stop":
		run, event, risk, okText, errText = s.docker.StopContainer, "docker.container.stop.requested", "medium", "已请求停止容器", "停止容器任务创建失败"
	case "restart":
		run, event, risk, okText, errText = s.docker.RestartContainer, "docker.container.restart.requested", "medium", "已请求重启容器", "重启容器任务创建失败"
	case "kill":
		run, event, risk, okText, errText = s.docker.KillContainer, "docker.container.kill.requested", "high", "已请求强制结束容器", "强制结束容器任务创建失败"
	default:
		writeError(w, http.StatusNotFound, "not_found", "未知操作")
		return
	}
	result, err := s.docker.StartJob(r.Context(), "docker.container."+action, okText, risk, dockerShortID(id), map[string]any{"container": dockerShortID(id), "action": action}, func(ctx context.Context, emit func(string, map[string]any)) error {
		emit("docker.job.output", map[string]any{"stream": "stdout", "message": okText})
		return run(ctx, id)
	})
	if err != nil {
		s.auditDocker(r, event, risk, errText, id, false, err)
		writeError(w, http.StatusBadGateway, "docker_unavailable", err.Error())
		return
	}
	s.auditDocker(r, event, risk, okText, id, true, nil)
	writeJSON(w, http.StatusAccepted, result)
}

func (s *Server) handleDockerPullImage(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, ctx.Session) {
		return
	}
	var req struct {
		Reference string `json:"reference"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	ref := strings.TrimSpace(req.Reference)
	if ref == "" {
		writeError(w, http.StatusBadRequest, "docker_image_ref_invalid", "镜像引用不能为空")
		return
	}
	result, err := s.docker.StartJob(r.Context(), "docker.image.pull", "拉取镜像", "medium", ref, map[string]any{"image": safelog.Text(ref, 120)}, func(ctx context.Context, emit func(string, map[string]any)) error {
		emit("docker.job.output", map[string]any{"stream": "stdout", "message": "开始拉取镜像 " + safelog.Text(ref, 120)})
		return s.docker.PullImage(ctx, ref)
	})
	if err != nil {
		s.auditDockerImage(r, "docker.image.pull.requested", "medium", "镜像拉取任务创建失败", ref, false, err)
		writeError(w, http.StatusBadGateway, "docker_unavailable", err.Error())
		return
	}
	s.auditDockerImage(r, "docker.image.pull.requested", "medium", "已请求拉取镜像", ref, true, nil)
	writeJSON(w, http.StatusAccepted, result)
}

func (s *Server) handleDockerRemoveImage(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, ctx.Session) {
		return
	}
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/docker/images/"), "/")
	if id == "" {
		writeError(w, http.StatusNotFound, "not_found", "未找到镜像")
		return
	}
	force := strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("force")), "true")
	result, err := s.docker.StartJob(r.Context(), "docker.image.remove", "删除镜像", "high", dockerShortID(id), map[string]any{"image": safelog.Text(dockerShortID(id), 120), "force": force}, func(ctx context.Context, emit func(string, map[string]any)) error {
		emit("docker.job.output", map[string]any{"stream": "stdout", "message": "开始删除镜像"})
		return s.docker.RemoveImage(ctx, id, force)
	})
	if err != nil {
		s.auditDockerImage(r, "docker.image.remove.requested", "high", "删除镜像任务创建失败", id, false, err)
		writeError(w, http.StatusBadGateway, "docker_unavailable", err.Error())
		return
	}
	s.auditDockerImage(r, "docker.image.remove.requested", "high", "已请求删除镜像", id, true, nil)
	writeJSON(w, http.StatusAccepted, result)
}

// auditDocker writes a redacted container-operation audit entry. The container
// id is shortened and never includes inspect payloads or env.
func (s *Server) auditDocker(r *http.Request, event, risk, summary, id string, ok bool, err error) {
	payload := map[string]any{"container": dockerShortID(id), "result": auditResult(ok)}
	if err != nil {
		payload["error"] = safelog.Error(err, 200)
	}
	_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{EventType: event, RiskLevel: risk, Summary: summary, Payload: payload})
}

// auditDockerImage writes a redacted image-operation audit entry. The reference
// is length-capped to avoid logging unexpected long input.
func (s *Server) auditDockerImage(r *http.Request, event, risk, summary, ref string, ok bool, err error) {
	payload := map[string]any{"image": safelog.Text(dockerShortID(ref), 120), "result": auditResult(ok)}
	if err != nil {
		payload["error"] = safelog.Error(err, 200)
	}
	_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{EventType: event, RiskLevel: risk, Summary: summary, Payload: payload})
}

func (s *Server) auditDockerHost(r *http.Request, event, risk, summary, target string, ok bool, err error) {
	payload := map[string]any{"target": safelog.Text(target, 120), "result": auditResult(ok)}
	if err != nil {
		payload["error"] = safelog.Error(err, 200)
	}
	_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{EventType: event, RiskLevel: risk, Summary: summary, Payload: payload})
}

func auditResult(ok bool) string {
	if ok {
		return "ok"
	}
	return "failed"
}

func dockerShortID(id string) string {
	id = strings.TrimPrefix(strings.TrimSpace(id), "sha256:")
	if len(id) > 12 && !strings.ContainsAny(id, "/:") {
		return id[:12]
	}
	return id
}
