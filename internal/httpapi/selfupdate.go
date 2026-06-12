package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"phantom-lancer/internal/auth"
	"phantom-lancer/internal/buildinfo"
	"phantom-lancer/internal/selfupdate"
	"phantom-lancer/internal/storage"
)

// SystemStatusPayload is the response shape for GET /api/system/status. It
// aggregates lightweight runtime signals (build info, supervisor liveness,
// data-dir path) that the UI polls periodically to render the health panel.
// It is intentionally narrower than /api/system/update/status — it never
// touches the DB, so it can be served very frequently without cost.
type SystemStatusPayload struct {
	Version    buildinfo.Info               `json:"version"`
	Supervisor *selfupdate.SupervisorStatus `json:"supervisor,omitempty"`
	DataDir    string                       `json:"dataDir,omitempty"`
	StartedAt  string                       `json:"startedAt,omitempty"`
	UptimeMs   int64                        `json:"uptimeMs,omitempty"`
}

func (s *Server) handleSystemVersion(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuth(w, r); !ok {
		return
	}
	writeJSON(w, http.StatusOK, buildinfo.Current())
}

func (s *Server) handleSystemStatus(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuth(w, r); !ok {
		return
	}
	status := SystemStatusPayload{
		Version:   buildinfo.Current(),
		DataDir:   s.dataDir,
		StartedAt: s.startedAt,
	}
	if s.startedAt != "" {
		if t, err := time.Parse(time.RFC3339Nano, s.startedAt); err == nil {
			status.UptimeMs = time.Since(t).Milliseconds()
		}
	}
	live := selfupdate.ResolveSupervisorStatus(s.dataDir)
	status.Supervisor = &live
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleSystemUpdateStatus(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuth(w, r); !ok {
		return
	}
	if s.updates == nil {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": false, "version": buildinfo.Current()})
		return
	}
	writeJSON(w, http.StatusOK, s.updates.Status(r.Context()))
}

func (s *Server) handleSystemUpdateCheck(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, ctx.Session) {
		return
	}
	if s.updates == nil {
		writeError(w, http.StatusServiceUnavailable, "updates_unavailable", "更新服务不可用")
		return
	}
	check, err := s.updates.Check(r.Context())
	_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
		EventType: "system.update.check",
		RiskLevel: "low",
		Summary:   "已检查系统更新",
		Payload: map[string]any{
			"currentVersion":  check.CurrentVersion,
			"latestVersion":   check.LatestVersion,
			"updateAvailable": check.UpdateAvailable,
			"canApply":        check.CanApply,
			"error":           check.ErrorMessage,
		},
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, "update_check_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"check": check, "status": s.updates.Status(r.Context())})
}

func (s *Server) handleSystemUpdateApply(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, ctx.Session) {
		return
	}
	if s.updates == nil {
		writeError(w, http.StatusServiceUnavailable, "updates_unavailable", "更新服务不可用")
		return
	}
	var req struct {
		TargetVersion              string `json:"targetVersion"`
		ReleaseID                  string `json:"releaseId"`
		ConfirmServiceInterruption bool   `json:"confirmServiceInterruption"`
		ConfirmTaskInterruption    bool   `json:"confirmTaskInterruption"`
		OwnerPassword              string `json:"ownerPassword"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if !s.verifyUpdatePassword(w, r, ctx, req.OwnerPassword) {
		return
	}
	result, err := s.updates.Apply(r.Context(), selfupdate.ApplyRequest{
		TargetVersion:              strings.TrimSpace(req.TargetVersion),
		ReleaseID:                  strings.TrimSpace(req.ReleaseID),
		ConfirmServiceInterruption: req.ConfirmServiceInterruption,
		ConfirmTaskInterruption:    req.ConfirmTaskInterruption,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, "update_apply_failed", err.Error())
		return
	}
	_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
		EventType: "system.update.start",
		RiskLevel: "high",
		Summary:   "已开始系统更新",
		Payload: map[string]any{
			"jobId":          result.Job.ID,
			"currentVersion": result.Job.CurrentVersion,
			"targetVersion":  result.Job.TargetVersion,
			"assetName":      result.Job.AssetName,
		},
	})
	writeJSON(w, http.StatusAccepted, result)
}

func (s *Server) handleSystemUpdateJobSubroutes(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/system/update/jobs/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, http.StatusNotFound, "update_job_not_found", "未找到更新任务")
		return
	}
	if s.updates == nil {
		writeError(w, http.StatusServiceUnavailable, "updates_unavailable", "更新服务不可用")
		return
	}
	if len(parts) == 1 {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "方法不允许")
			return
		}
		job, err := s.store.GetSystemUpdateJob(r.Context(), parts[0])
		if err != nil {
			writeError(w, http.StatusNotFound, "update_job_not_found", "未找到更新任务")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"job": job})
		return
	}
	if len(parts) == 2 && parts[1] == "cancel" {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "方法不允许")
			return
		}
		if !s.requireCSRF(w, r, ctx.Session) {
			return
		}
		job, err := s.updates.Cancel(r.Context(), parts[0])
		if err != nil {
			code := http.StatusBadRequest
			if errors.Is(err, storage.ErrNotFound) {
				code = http.StatusNotFound
			}
			writeError(w, code, "update_cancel_failed", err.Error())
			return
		}
		_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
			EventType: "system.update.cancel",
			RiskLevel: "medium",
			Summary:   "已取消系统更新",
			Payload:   map[string]any{"jobId": job.ID, "targetVersion": job.TargetVersion, "phase": job.Phase},
		})
		writeJSON(w, http.StatusOK, map[string]any{"job": job})
		return
	}
	if len(parts) == 2 && parts[1] == "rollback" {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "方法不允许")
			return
		}
		if !s.requireCSRF(w, r, ctx.Session) {
			return
		}
		var req struct {
			OwnerPassword string `json:"ownerPassword"`
		}
		if !decodeJSON(w, r, &req) {
			return
		}
		if !s.verifyUpdatePassword(w, r, ctx, req.OwnerPassword) {
			return
		}
		job, execPath, err := s.updates.Rollback(r.Context(), parts[0])
		if err != nil {
			code := http.StatusBadRequest
			if errors.Is(err, storage.ErrNotFound) {
				code = http.StatusNotFound
			}
			writeError(w, code, "update_rollback_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"job": job, "execPath": execPath})
		return
	}
	writeError(w, http.StatusNotFound, "update_job_not_found", "未找到更新任务")
}

func (s *Server) verifyUpdatePassword(w http.ResponseWriter, r *http.Request, ctx sessionContext, password string) bool {
	owner, err := s.store.GetOwnerByID(r.Context(), ctx.Session.OwnerID)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "登录已过期")
		return false
	}
	key := "update:" + owner.Username
	ip := clientIP(r)
	if decision := s.updateConfirms.Check(key, ip, time.Now()); decision.Limited {
		_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
			EventType: "system.update.confirm.rate_limited",
			RiskLevel: "medium",
			Summary:   "系统更新确认已被限流",
			Payload: map[string]any{
				"ip":           ip,
				"dimension":    decision.Dimension,
				"backoffUntil": decision.BackoffUntil.UTC().Format(time.RFC3339Nano),
			},
		})
		writeError(w, http.StatusTooManyRequests, "auth_backoff", "确认失败次数过多，请稍后再试")
		return false
	}
	if !auth.VerifyPassword(owner.PasswordHash, password) {
		events := s.updateConfirms.RecordFailure(key, ip, time.Now())
		for _, event := range events {
			_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
				EventType: "system.update.confirm.backoff_started",
				RiskLevel: "medium",
				Summary:   "系统更新确认失败触发退避",
				Payload: map[string]any{
					"ip":           ip,
					"dimension":    event.Dimension,
					"backoffUntil": event.BackoffUntil.UTC().Format(time.RFC3339Nano),
				},
			})
		}
		writeError(w, http.StatusUnauthorized, "invalid_password", "管理员密码不正确")
		return false
	}
	s.updateConfirms.RecordSuccess(key)
	return true
}
