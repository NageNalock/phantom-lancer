package httpapi

import (
	"context"
	"net/http"
	"strings"
	"time"

	"phantom-lancer/internal/objectstore"
	"phantom-lancer/internal/safelog"
	"phantom-lancer/internal/storage"
)

// Object Storage Profiles are a global, cross-module capability (Images, Docker
// Registry). They live under /api/object-storage/* rather than any single
// module. Secrets are never returned in responses or written to audit/logs.

func (s *Server) handleListObjectStorageProfiles(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuth(w, r); !ok {
		return
	}
	profiles, err := s.store.ListObjectStorageProfiles(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": profiles})
}

type objectStorageProfileRequest struct {
	Name            string `json:"name"`
	ProviderLabel   string `json:"providerLabel"`
	Bucket          string `json:"bucket"`
	Region          string `json:"region"`
	Endpoint        string `json:"endpoint"`
	ForcePathStyle  bool   `json:"forcePathStyle"`
	AccessKeyID     string `json:"accessKeyId"`
	SecretAccessKey string `json:"secretAccessKey"`
	SessionToken    string `json:"sessionToken"`
	ClearSecret     bool   `json:"clearSecret"`
}

func (req objectStorageProfileRequest) toProfile() storage.ObjectStorageProfile {
	return storage.ObjectStorageProfile{
		Name:            req.Name,
		ProviderLabel:   req.ProviderLabel,
		Bucket:          req.Bucket,
		Region:          req.Region,
		Endpoint:        req.Endpoint,
		ForcePathStyle:  req.ForcePathStyle,
		AccessKeyID:     req.AccessKeyID,
		SecretAccessKey: req.SecretAccessKey,
		SessionToken:    req.SessionToken,
	}
}

func (req objectStorageProfileRequest) secretProvided() bool {
	return strings.TrimSpace(req.AccessKeyID) != "" || strings.TrimSpace(req.SecretAccessKey) != "" || strings.TrimSpace(req.SessionToken) != ""
}

func validObjectStorageProfileRequest(req objectStorageProfileRequest) (string, bool) {
	if strings.TrimSpace(req.Bucket) == "" {
		return "bucket 不能为空", false
	}
	if strings.TrimSpace(req.Endpoint) == "" {
		return "endpoint 不能为空", false
	}
	if !validOptionalURL(req.Endpoint) {
		return "endpoint 必须是合法 URL", false
	}
	return "", true
}

func (s *Server) handleCreateObjectStorageProfile(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, ctx.Session) {
		return
	}
	var req objectStorageProfileRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if msg, ok := validObjectStorageProfileRequest(req); !ok {
		writeError(w, http.StatusBadRequest, "object_storage_profile_invalid", msg)
		return
	}
	created, err := s.store.CreateObjectStorageProfile(r.Context(), req.toProfile())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
		EventType: "object_storage.profile.created",
		RiskLevel: "medium",
		Summary:   "已创建对象存储 profile",
		Payload:   map[string]any{"profileId": created.ID, "endpoint": objectstore.EndpointLabel(created.Endpoint), "bucket": created.Bucket, "hasCredentials": created.HasCredentials},
	})
	writeJSON(w, http.StatusCreated, map[string]any{"profile": created})
}

func (s *Server) handleObjectStorageProfileSubroutes(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, ctx.Session) {
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/object-storage/profiles/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, http.StatusNotFound, "not_found", "未找到对象存储 profile")
		return
	}
	id := parts[0]
	if len(parts) == 2 {
		switch parts[1] {
		case "test":
			if r.Method != http.MethodPost {
				writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "方法不允许")
				return
			}
			s.testObjectStorageProfile(w, r, id)
		case "rotate-secret":
			if r.Method != http.MethodPost {
				writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "方法不允许")
				return
			}
			s.rotateObjectStorageProfileSecret(w, r, id)
		default:
			writeError(w, http.StatusNotFound, "not_found", "未找到对象存储 profile 路由")
		}
		return
	}
	if len(parts) != 1 {
		writeError(w, http.StatusNotFound, "not_found", "未找到对象存储 profile 路由")
		return
	}
	switch r.Method {
	case http.MethodGet:
		profile, err := s.store.GetObjectStorageProfile(r.Context(), id)
		if err != nil {
			writeStoreError(w, err, "对象存储 profile 不存在")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"profile": profile})
	case http.MethodPatch:
		var req objectStorageProfileRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		if msg, ok := validObjectStorageProfileRequest(req); !ok {
			writeError(w, http.StatusBadRequest, "object_storage_profile_invalid", msg)
			return
		}
		profile := req.toProfile()
		profile.ID = id
		updated, err := s.store.UpdateObjectStorageProfile(r.Context(), profile, req.secretProvided(), req.ClearSecret)
		if err != nil {
			writeStoreError(w, err, "对象存储 profile 更新失败")
			return
		}
		_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
			EventType: "object_storage.profile.updated",
			RiskLevel: "medium",
			Summary:   "已更新对象存储 profile",
			Payload:   map[string]any{"profileId": updated.ID, "endpoint": objectstore.EndpointLabel(updated.Endpoint), "bucket": updated.Bucket, "updatedSecret": req.secretProvided(), "clearedSecret": req.ClearSecret},
		})
		writeJSON(w, http.StatusOK, map[string]any{"profile": updated})
	case http.MethodDelete:
		refs, err := s.store.ObjectStorageProfileReferencedBy(r.Context(), id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
		if s.stockV2 != nil {
			referenced, referenceErr := s.stockV2.AgentTraceObjectStorageProfileReferenced(r.Context(), id)
			if referenceErr != nil {
				writeError(w, http.StatusInternalServerError, "internal_error", referenceErr.Error())
				return
			}
			if referenced {
				refs = append(refs, "stockv2_agent_traces")
			}
		}
		if len(refs) > 0 {
			writeError(w, http.StatusConflict, "object_storage_profile_in_use", "profile 仍被以下模块引用："+strings.Join(refs, ", "))
			return
		}
		if err := s.store.DeleteObjectStorageProfile(r.Context(), id); err != nil {
			writeStoreError(w, err, "对象存储 profile 删除失败")
			return
		}
		_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
			EventType: "object_storage.profile.deleted",
			RiskLevel: "medium",
			Summary:   "已删除对象存储 profile",
			Payload:   map[string]any{"profileId": id},
		})
		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "方法不允许")
	}
}

func (s *Server) testObjectStorageProfile(w http.ResponseWriter, r *http.Request, id string) {
	profile, err := s.store.GetObjectStorageProfile(r.Context(), id)
	if err != nil {
		writeStoreError(w, err, "对象存储 profile 不存在")
		return
	}
	client, err := objectstore.New(profile)
	if err != nil {
		_ = s.store.SetObjectStorageProfileTestResult(r.Context(), id, false, safelog.Error(err, 240))
		writeError(w, http.StatusBadRequest, "object_storage_profile_invalid", err.Error())
		return
	}
	testCtx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	started := time.Now()
	if err := client.Test(testCtx, "phantom-lancer/connection-test"); err != nil {
		_ = s.store.SetObjectStorageProfileTestResult(r.Context(), id, false, safelog.Error(err, 240))
		if s.log != nil {
			s.log.Warn("object storage profile test failed", "profile_id", id, "endpoint", client.EndpointLabel(), "bucket", client.Bucket(), "latency_ms", time.Since(started).Milliseconds(), "error", safelog.Error(err, 200))
		}
		_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
			EventType: "object_storage.profile.tested",
			RiskLevel: "medium",
			Summary:   "对象存储连接测试失败",
			Payload:   map[string]any{"profileId": id, "endpoint": client.EndpointLabel(), "bucket": client.Bucket(), "error": safelog.Error(err, 240)},
		})
		writeError(w, http.StatusBadGateway, "object_storage_test_failed", err.Error())
		return
	}
	_ = s.store.SetObjectStorageProfileTestResult(r.Context(), id, true, "")
	_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
		EventType: "object_storage.profile.tested",
		RiskLevel: "low",
		Summary:   "对象存储连接测试通过",
		Payload:   map[string]any{"profileId": id, "endpoint": client.EndpointLabel(), "bucket": client.Bucket()},
	})
	profile, _ = s.store.GetObjectStorageProfile(r.Context(), id)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "profile": profile})
}

func (s *Server) rotateObjectStorageProfileSecret(w http.ResponseWriter, r *http.Request, id string) {
	var req objectStorageProfileRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.AccessKeyID) == "" || strings.TrimSpace(req.SecretAccessKey) == "" {
		writeError(w, http.StatusBadRequest, "object_storage_profile_invalid", "access key 与 secret key 不能为空")
		return
	}
	existing, err := s.store.GetObjectStorageProfile(r.Context(), id)
	if err != nil {
		writeStoreError(w, err, "对象存储 profile 不存在")
		return
	}
	existing.AccessKeyID = req.AccessKeyID
	existing.SecretAccessKey = req.SecretAccessKey
	existing.SessionToken = req.SessionToken
	updated, err := s.store.UpdateObjectStorageProfile(r.Context(), existing, true, false)
	if err != nil {
		writeStoreError(w, err, "对象存储 profile 凭据轮换失败")
		return
	}
	_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
		EventType: "object_storage.profile.secret_rotated",
		RiskLevel: "medium",
		Summary:   "已轮换对象存储 profile 凭据",
		Payload:   map[string]any{"profileId": updated.ID},
	})
	writeJSON(w, http.StatusOK, map[string]any{"profile": updated})
}
