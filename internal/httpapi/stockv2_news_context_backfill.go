package httpapi

import (
	"context"
	"net/http"

	"phantom-lancer/internal/stockv2"
	"phantom-lancer/internal/storage"
)

func (s *Server) handleStockV2PreviewNewsContextBackfill(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuth(w, r); !ok {
		return
	}
	result, err := s.stockV2.PreviewNewsContextBackfill(r.Context())
	if err != nil {
		writeStockV2NewsContextError(w, "stockv2_news_context_backfill_preview_failed", err)
		return
	}
	s.writeJSON(w, result)
}

func (s *Server) handleStockV2GetNewsContextBackfill(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuth(w, r); !ok {
		return
	}
	result, err := s.stockV2.GetNewsContextBackfill(r.Context())
	if err != nil {
		writeStockV2NewsContextError(w, "stockv2_news_context_backfill_failed", err)
		return
	}
	s.writeJSON(w, result)
}

func (s *Server) handleStockV2StartNewsContextBackfill(w http.ResponseWriter, r *http.Request) {
	s.handleStockV2NewsContextBackfillAction(w, r, "start")
}

func (s *Server) handleStockV2PauseNewsContextBackfill(w http.ResponseWriter, r *http.Request) {
	s.handleStockV2NewsContextBackfillAction(w, r, "pause")
}

func (s *Server) handleStockV2ResumeNewsContextBackfill(w http.ResponseWriter, r *http.Request) {
	s.handleStockV2NewsContextBackfillAction(w, r, "resume")
}

func (s *Server) handleStockV2RetryNewsContextBackfill(w http.ResponseWriter, r *http.Request) {
	s.handleStockV2NewsContextBackfillAction(w, r, "retry")
}

func (s *Server) handleStockV2NewsContextBackfillAction(w http.ResponseWriter, r *http.Request, action string) {
	session, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, session.Session) {
		return
	}
	var (
		result stockv2.NewsContextBackfill
		err    error
	)
	switch action {
	case "start":
		r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
		var req stockv2.RequestStartNewsContextBackfill
		if err := stockV2DecodeNewsContextJSON(r, &req, true); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request_body", "invalid request body")
			return
		}
		req.RequestedBy = session.Session.OwnerID
		result, err = s.stockV2.StartNewsContextBackfill(r.Context(), req)
	case "pause":
		result, err = s.stockV2.PauseNewsContextBackfill(r.Context())
	case "resume":
		result, err = s.stockV2.ResumeNewsContextBackfill(r.Context())
	case "retry":
		result, err = s.stockV2.RetryNewsContextBackfill(r.Context())
	}
	if err != nil {
		writeStockV2NewsContextError(w, "stockv2_news_context_backfill_action_failed", err)
		return
	}
	stockV2AuditNewsContextBackfillAction(r.Context(), s, session.Session.OwnerID, action, result)
	s.writeJSON(w, result)
}

func stockV2AuditNewsContextBackfillAction(ctx context.Context, s *Server, ownerID, action string, item stockv2.NewsContextBackfill) {
	summary := map[string]string{
		"start":  "启动消息脉络历史补处理",
		"pause":  "暂停消息脉络历史补处理",
		"resume": "继续消息脉络历史补处理",
		"retry":  "重试消息脉络历史补处理",
	}[action]
	_, _ = s.store.AddAudit(ctx, storage.AuditEvent{
		EventType: "stockv2_news_context_backfill_" + action,
		RiskLevel: "medium",
		Summary:   summary,
		Payload: map[string]any{
			"ownerId": ownerID, "backfillId": item.ID, "status": item.Status,
		},
	})
}
