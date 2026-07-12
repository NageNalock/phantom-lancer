package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"phantom-lancer/internal/stockv2"
	"phantom-lancer/internal/storage"
)

const (
	stockV2NewsContextRunKindAggregation = "aggregation"
	stockV2NewsContextRunKindCleanup     = "cleanup"
)

func (s *Server) handleStockV2GetNewsContextSummary(w http.ResponseWriter, r *http.Request) {
	result, err := s.stockV2.GetNewsContextSummary(r.Context())
	if err != nil {
		writeStockV2NewsContextError(w, "stockv2_news_context_summary_failed", err)
		return
	}
	s.writeJSON(w, result)
}

func (s *Server) handleStockV2GetNewsContextConfig(w http.ResponseWriter, r *http.Request) {
	result, err := s.stockV2.GetNewsContextConfig(r.Context())
	if err != nil {
		writeStockV2NewsContextError(w, "stockv2_news_context_config_failed", err)
		return
	}
	s.writeJSON(w, result)
}

func (s *Server) handleStockV2UpdateNewsContextConfig(w http.ResponseWriter, r *http.Request) {
	session, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, session.Session) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	var req stockv2.RequestUpdateNewsContextConfig
	if err := stockV2DecodeNewsContextJSON(r, &req, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_body", "invalid request body")
		return
	}
	result, err := s.stockV2.PatchNewsContextConfig(r.Context(), req)
	if err != nil {
		writeStockV2NewsContextError(w, "stockv2_news_context_config_failed", err)
		return
	}
	_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
		EventType: "stockv2_news_context_config_updated", RiskLevel: "medium", Summary: "更新消息脉络定时归纳与安全清理配置",
		Payload: map[string]any{"ownerId": session.Session.OwnerID, "enabled": result.Enabled, "autoCleanupEnabled": result.AutoCleanupEnabled},
	})
	s.writeJSON(w, result)
}

func (s *Server) handleStockV2ListNewsThreads(w http.ResponseWriter, r *http.Request) {
	filter, err := stockV2NewsThreadFilterFromRequest(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_filter", err.Error())
		return
	}
	items, err := s.stockV2.ListNewsThreads(r.Context(), filter)
	if err != nil {
		writeStockV2NewsContextError(w, "stockv2_news_context_themes_failed", err)
		return
	}
	total, err := s.stockV2.CountNewsThreads(r.Context(), filter)
	if err != nil {
		writeStockV2NewsContextError(w, "stockv2_news_context_themes_count_failed", err)
		return
	}
	s.writeJSON(w, map[string]any{"items": items, "total": total})
}

func (s *Server) handleStockV2GetNewsThread(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid_theme_id", "theme id is required")
		return
	}
	result, err := s.stockV2.GetNewsThreadDetail(r.Context(), id)
	if err != nil {
		writeStockV2NewsContextError(w, "stockv2_news_context_theme_failed", err)
		return
	}
	s.writeJSON(w, result)
}

func (s *Server) handleStockV2GetNewsContextRotationSignals(w http.ResponseWriter, r *http.Request) {
	result, err := s.stockV2.NewsContextRotationSignals(r.Context())
	if err != nil {
		writeStockV2NewsContextError(w, "stockv2_news_context_rotation_failed", err)
		return
	}
	s.writeJSON(w, result)
}

func (s *Server) handleStockV2ListNewsContextRuns(w http.ResponseWriter, r *http.Request) {
	kind := strings.TrimSpace(r.URL.Query().Get("kind"))
	if kind == "" {
		kind = stockV2NewsContextRunKindAggregation
	}
	switch kind {
	case stockV2NewsContextRunKindAggregation:
		filter, err := stockV2NewsContextRunFilterFromRequest(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_filter", err.Error())
			return
		}
		items, err := s.stockV2.ListNewsContextRuns(r.Context(), filter)
		if err != nil {
			writeStockV2NewsContextError(w, "stockv2_news_context_runs_failed", err)
			return
		}
		total, err := s.stockV2.CountNewsContextRuns(r.Context(), filter)
		if err != nil {
			writeStockV2NewsContextError(w, "stockv2_news_context_runs_count_failed", err)
			return
		}
		s.writeJSON(w, map[string]any{"items": items, "total": total})
	case stockV2NewsContextRunKindCleanup:
		filter, err := stockV2NewsContextCleanupRunFilterFromRequest(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_filter", err.Error())
			return
		}
		items, err := s.stockV2.ListNewsContextCleanupRuns(r.Context(), filter)
		if err != nil {
			writeStockV2NewsContextError(w, "stockv2_news_context_cleanup_runs_failed", err)
			return
		}
		total, err := s.stockV2.CountNewsContextCleanupRuns(r.Context(), filter)
		if err != nil {
			writeStockV2NewsContextError(w, "stockv2_news_context_cleanup_runs_count_failed", err)
			return
		}
		s.writeJSON(w, map[string]any{"items": items, "total": total})
	default:
		writeError(w, http.StatusBadRequest, "invalid_filter", "invalid run kind")
	}
}

func (s *Server) handleStockV2StartNewsContextRun(w http.ResponseWriter, r *http.Request) {
	session, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, session.Session) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	var req stockv2.RequestStartNewsContextRun
	if err := stockV2DecodeNewsContextJSON(r, &req, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_body", "invalid request body")
		return
	}
	if err := stockV2ValidateNewsContextRunRequest(req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_body", err.Error())
		return
	}
	req.RequestedBy = session.Session.OwnerID
	result, err := s.stockV2.StartNewsContextRun(r.Context(), req)
	if err != nil {
		writeStockV2NewsContextError(w, "stockv2_news_context_run_failed", err)
		return
	}
	_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
		EventType: "stockv2_news_context_run_started", RiskLevel: "low", Summary: "手动触发消息脉络归纳",
		Payload: map[string]any{"ownerId": session.Session.OwnerID, "runId": result.ID, "windowType": result.WindowType},
	})
	s.writeJSON(w, result)
}

func (s *Server) handleStockV2RetryNewsContextRun(w http.ResponseWriter, r *http.Request) {
	session, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, session.Session) {
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid_run_id", "run id is required")
		return
	}
	result, err := s.stockV2.RetryNewsContextRun(r.Context(), id)
	if errors.Is(err, stockv2.ErrNewsContextRunNotFound) {
		cleanupRun, cleanupErr := s.stockV2.RetryNewsContextCleanupRun(r.Context(), id)
		if cleanupErr == nil {
			_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
				EventType: "stockv2_news_context_run_retried", RiskLevel: "medium", Summary: "重试消息脉络安全清理",
				Payload: map[string]any{"ownerId": session.Session.OwnerID, "runId": id, "cleanupRunId": cleanupRun.ID},
			})
			s.writeJSON(w, cleanupRun)
			return
		}
		err = cleanupErr
	}
	if err != nil {
		writeStockV2NewsContextError(w, "stockv2_news_context_retry_failed", err)
		return
	}
	_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
		EventType: "stockv2_news_context_run_retried", RiskLevel: "medium", Summary: "重试消息脉络归纳或清理",
		Payload: map[string]any{"ownerId": session.Session.OwnerID, "runId": id},
	})
	s.writeJSON(w, result)
}

func (s *Server) handleStockV2StartNewsContextCleanupRun(w http.ResponseWriter, r *http.Request) {
	session, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, session.Session) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	var req stockv2.RequestStartNewsContextCleanup
	if err := stockV2DecodeNewsContextJSON(r, &req, true); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_body", "invalid request body")
		return
	}
	if _, err := stockV2NewsContextOptionalTime(req.Before, "before"); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_body", err.Error())
		return
	}
	req.RequestedBy = session.Session.OwnerID
	result, err := s.stockV2.StartNewsContextCleanupRun(r.Context(), req)
	if err != nil {
		writeStockV2NewsContextError(w, "stockv2_news_context_cleanup_failed", err)
		return
	}
	_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{
		EventType: "stockv2_news_context_cleanup_started", RiskLevel: "high", Summary: "手动触发消息脉络安全清理",
		Payload: map[string]any{"ownerId": session.Session.OwnerID, "cleanupRunId": result.ID, "contextRunId": result.ContextRunID},
	})
	s.writeJSON(w, result)
}

func stockV2NewsThreadFilterFromRequest(r *http.Request) (stockv2.NewsThreadListFilter, error) {
	query := r.URL.Query()
	limit, offset, since, until, err := stockV2NewsContextPageAndWindow(r)
	if err != nil {
		return stockv2.NewsThreadListFilter{}, err
	}
	status := strings.TrimSpace(query.Get("status"))
	if status != "" && !stockV2HTTPValueIn(status, stockv2.NewsThreadStatusActive, stockv2.NewsThreadStatusDormant, stockv2.NewsThreadStatusMerged, stockv2.NewsThreadStatusArchived) {
		return stockv2.NewsThreadListFilter{}, errors.New("invalid theme status")
	}
	stage := strings.TrimSpace(query.Get("stage"))
	if stage != "" && !stockV2HTTPValueIn(stage,
		stockv2.NewsThreadStageEmerging, stockv2.NewsThreadStageSpreading, stockv2.NewsThreadStageAccelerating,
		stockv2.NewsThreadStageOverheated, stockv2.NewsThreadStageDiverging, stockv2.NewsThreadStageRetreating,
		stockv2.NewsThreadStageDormant, stockv2.NewsThreadStageRestarting,
	) {
		return stockv2.NewsThreadListFilter{}, errors.New("invalid theme stage")
	}
	reviewStatus := strings.TrimSpace(query.Get("reviewStatus"))
	if reviewStatus != "" && !stockV2NewsContextValidReviewStatus(reviewStatus) {
		return stockv2.NewsThreadListFilter{}, errors.New("invalid review status")
	}
	indexStatus := strings.TrimSpace(query.Get("indexStatus"))
	if indexStatus != "" && !stockV2NewsContextValidIndexStatus(indexStatus) {
		return stockv2.NewsThreadListFilter{}, errors.New("invalid index status")
	}
	return stockv2.NewsThreadListFilter{
		Status: status, Stage: stage, ReviewStatus: reviewStatus, IndexStatus: indexStatus,
		Query: strings.TrimSpace(query.Get("q")), Affected: strings.TrimSpace(query.Get("affected")),
		Since: since, Until: until, Limit: limit, Offset: offset,
	}, nil
}

func stockV2NewsContextRunFilterFromRequest(r *http.Request) (stockv2.NewsContextRunListFilter, error) {
	query := r.URL.Query()
	limit, offset, since, until, err := stockV2NewsContextPageAndWindow(r)
	if err != nil {
		return stockv2.NewsContextRunListFilter{}, err
	}
	windowType := strings.TrimSpace(query.Get("windowType"))
	if windowType != "" && !stockV2HTTPValueIn(windowType, stockv2.NewsContextWindowHourly, stockv2.NewsContextWindowFourHour, stockv2.NewsContextWindowDaily) {
		return stockv2.NewsContextRunListFilter{}, errors.New("invalid window type")
	}
	triggerType := strings.TrimSpace(query.Get("triggerType"))
	if triggerType != "" && !stockV2HTTPValueIn(triggerType, stockv2.NewsContextTriggerScheduled, stockv2.NewsContextTriggerManual, stockv2.NewsContextTriggerRetry) {
		return stockv2.NewsContextRunListFilter{}, errors.New("invalid trigger type")
	}
	status := strings.TrimSpace(query.Get("status"))
	if status != "" && !stockV2HTTPValueIn(status,
		stockv2.NewsContextRunStatusPending, stockv2.NewsContextRunStatusRunning, stockv2.NewsContextRunStatusWaitingReview,
		stockv2.NewsContextRunStatusCompleted, stockv2.NewsContextRunStatusFailed,
	) {
		return stockv2.NewsContextRunListFilter{}, errors.New("invalid aggregation run status")
	}
	reviewStatus := strings.TrimSpace(query.Get("reviewStatus"))
	if reviewStatus != "" && !stockV2NewsContextValidReviewStatus(reviewStatus) {
		return stockv2.NewsContextRunListFilter{}, errors.New("invalid review status")
	}
	return stockv2.NewsContextRunListFilter{
		WindowType: windowType, TriggerType: triggerType, Status: status, ReviewStatus: reviewStatus,
		Since: since, Until: until, Limit: limit, Offset: offset,
	}, nil
}

func stockV2NewsContextCleanupRunFilterFromRequest(r *http.Request) (stockv2.NewsContextCleanupRunListFilter, error) {
	query := r.URL.Query()
	limit, offset, since, until, err := stockV2NewsContextPageAndWindow(r)
	if err != nil {
		return stockv2.NewsContextCleanupRunListFilter{}, err
	}
	status := strings.TrimSpace(query.Get("status"))
	if status != "" && !stockV2HTTPValueIn(status,
		stockv2.NewsContextCleanupPending, stockv2.NewsContextCleanupRunning, stockv2.NewsContextCleanupCompleted,
		stockv2.NewsContextCleanupPartial, stockv2.NewsContextCleanupFailed,
	) {
		return stockv2.NewsContextCleanupRunListFilter{}, errors.New("invalid cleanup run status")
	}
	return stockv2.NewsContextCleanupRunListFilter{Status: status, Since: since, Until: until, Limit: limit, Offset: offset}, nil
}

func stockV2NewsContextPageAndWindow(r *http.Request) (int, int, time.Time, time.Time, error) {
	query := r.URL.Query()
	limit, err := stockV2PositiveInt(query.Get("limit"), 50)
	if err != nil || limit > 200 {
		return 0, 0, time.Time{}, time.Time{}, errors.New("invalid limit")
	}
	offset, err := stockV2NonNegativeInt(query.Get("offset"), 0)
	if err != nil {
		return 0, 0, time.Time{}, time.Time{}, errors.New("invalid offset")
	}
	since, err := stockV2NewsContextOptionalTime(query.Get("since"), "since")
	if err != nil {
		return 0, 0, time.Time{}, time.Time{}, err
	}
	until, err := stockV2NewsContextOptionalTime(query.Get("until"), "until")
	if err != nil {
		return 0, 0, time.Time{}, time.Time{}, err
	}
	if !since.IsZero() && !until.IsZero() && since.After(until) {
		return 0, 0, time.Time{}, time.Time{}, errors.New("since must not be after until")
	}
	return limit, offset, since, until, nil
}

func stockV2NewsContextOptionalTime(raw, name string) (time.Time, error) {
	if strings.TrimSpace(raw) == "" {
		return time.Time{}, nil
	}
	value := parseStockV2NewsTime(raw)
	if value.IsZero() {
		return time.Time{}, errors.New("invalid " + name)
	}
	return value, nil
}

func stockV2NewsContextValidReviewStatus(value string) bool {
	return stockV2HTTPValueIn(value,
		stockv2.NewsContextReviewNotRequired, stockv2.NewsContextReviewPending, stockv2.NewsContextReviewRunning,
		stockv2.NewsContextReviewCompleted, stockv2.NewsContextReviewFailed,
	)
}

func stockV2NewsContextValidIndexStatus(value string) bool {
	return stockV2HTTPValueIn(value,
		stockv2.NewsContextIndexPending, stockv2.NewsContextIndexReady, stockv2.NewsContextIndexStale, stockv2.NewsContextIndexFailed,
	)
}

func stockV2DecodeNewsContextJSON(r *http.Request, dst any, optional bool) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		if optional && errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("request body must contain one JSON object")
		}
		return err
	}
	return nil
}

func stockV2ValidateNewsContextRunRequest(req stockv2.RequestStartNewsContextRun) error {
	if !stockV2HTTPValueIn(strings.TrimSpace(req.WindowType), stockv2.NewsContextWindowHourly, stockv2.NewsContextWindowFourHour, stockv2.NewsContextWindowDaily) {
		return errors.New("invalid window type")
	}
	startAt, err := stockV2NewsContextOptionalTime(req.StartAt, "startAt")
	if err != nil {
		return err
	}
	endAt, err := stockV2NewsContextOptionalTime(req.EndAt, "endAt")
	if err != nil {
		return err
	}
	if !startAt.IsZero() && !endAt.IsZero() && !endAt.After(startAt) {
		return errors.New("startAt must be before endAt")
	}
	return nil
}

func writeStockV2NewsContextError(w http.ResponseWriter, code string, err error) {
	status := stockV2NewsContextHTTPStatus(err)
	message := err.Error()
	if errors.Is(err, stockv2.ErrNewsContextPrerequisite) {
		message = "请先为消息脉络归纳、组合复核和主题向量配置可用模型"
	}
	if status == http.StatusInternalServerError {
		message = "消息脉络服务暂时不可用"
	}
	writeError(w, status, code, message)
}

func stockV2NewsContextHTTPStatus(err error) int {
	switch {
	case errors.Is(err, stockv2.ErrNewsThreadNotFound),
		errors.Is(err, stockv2.ErrNewsContextRunNotFound),
		errors.Is(err, stockv2.ErrNewsContextCleanupNotFound):
		return http.StatusNotFound
	case errors.Is(err, stockv2.ErrNewsContextAlreadyRunning),
		errors.Is(err, stockv2.ErrNewsContextCleanupRunning),
		errors.Is(err, stockv2.ErrNewsContextReviewIncomplete):
		return http.StatusConflict
	case errors.Is(err, stockv2.ErrNewsContextPrerequisite):
		return http.StatusConflict
	case errors.Is(err, stockv2.ErrInvalidNewsContextInput),
		errors.Is(err, stockv2.ErrInvalidNewsContextResult),
		errors.Is(err, stockv2.ErrNewsContextFeatureDisabled),
		errors.Is(err, stockv2.ErrNewsContextCleanupDisabled):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}
