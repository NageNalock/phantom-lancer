package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"phantom-lancer/internal/stockv2"
	"phantom-lancer/internal/storage"
)

func (s *Server) handleStockV2GetOpportunityMarketScanConfig(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuth(w, r); !ok {
		return
	}
	status, err := s.stockV2.GetOpportunityMarketScanStatus(r.Context())
	if err != nil {
		writeOpportunityMarketScanError(w, err)
		return
	}
	s.writeJSON(w, status)
}

func (s *Server) handleStockV2UpdateOpportunityMarketScanConfig(w http.ResponseWriter, r *http.Request) {
	session, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, session.Session) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
	var req stockv2.RequestUpdateOpportunityMarketScanConfig
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_body", "invalid request body")
		return
	}
	status, err := s.stockV2.UpdateOpportunityMarketScanConfig(r.Context(), req)
	if err != nil {
		writeOpportunityMarketScanError(w, err)
		return
	}
	_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{EventType: "stockv2_opportunity_market_scan_config_updated", RiskLevel: "medium", Summary: "更新机会发现市场扫描配置", Payload: map[string]any{"ownerId": session.Session.OwnerID, "enabled": status.Config.Enabled}})
	s.writeJSON(w, status)
}

func (s *Server) handleStockV2ProbeOpportunityMarketFundFlow(w http.ResponseWriter, r *http.Request) {
	if session, ok := s.requireAuth(w, r); !ok || !s.requireCSRF(w, r, session.Session) {
		return
	}
	s.writeJSON(w, s.stockV2.ProbeOpportunityMarketFundFlow(r.Context()))
}

func (s *Server) handleStockV2ListOpportunityMarketScanRuns(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuth(w, r); !ok {
		return
	}
	limit, err := stockV2PositiveInt(r.URL.Query().Get("limit"), 30)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_limit", err.Error())
		return
	}
	offset, err := stockV2NonNegativeInt(r.URL.Query().Get("offset"), 0)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_offset", err.Error())
		return
	}
	filter := stockv2.OpportunityMarketScanRunListFilter{Status: strings.TrimSpace(r.URL.Query().Get("status")), Limit: limit, Offset: offset}
	items, err := s.stockV2.ListOpportunityMarketScanRuns(r.Context(), filter)
	if err != nil {
		writeOpportunityMarketScanError(w, err)
		return
	}
	total, err := s.stockV2.CountOpportunityMarketScanRuns(r.Context(), filter)
	if err != nil {
		writeOpportunityMarketScanError(w, err)
		return
	}
	s.writeJSON(w, map[string]any{"items": items, "total": total, "limit": limit, "offset": offset})
}

func (s *Server) handleStockV2StartOpportunityMarketScanRun(w http.ResponseWriter, r *http.Request) {
	session, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, session.Session) {
		return
	}
	run, err := s.stockV2.StartOpportunityMarketScan(r.Context(), stockv2.OpportunityMarketScanTriggerManual, session.Session.OwnerID)
	if err != nil {
		writeOpportunityMarketScanError(w, err)
		return
	}
	auditOpportunityMarketScan(r, s, session.Session.OwnerID, "started", run)
	s.writeJSON(w, run)
}

func (s *Server) handleStockV2GetOpportunityMarketScanRun(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuth(w, r); !ok {
		return
	}
	run, err := s.stockV2.GetOpportunityMarketScanRun(r.Context(), r.PathValue("id"))
	if err != nil {
		writeOpportunityMarketScanError(w, err)
		return
	}
	s.writeJSON(w, run)
}

func (s *Server) handleStockV2ListOpportunityMarketScanCandidates(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAuth(w, r); !ok {
		return
	}
	if _, err := s.stockV2.GetOpportunityMarketScanRun(r.Context(), r.PathValue("id")); err != nil {
		writeOpportunityMarketScanError(w, err)
		return
	}
	limit, err := stockV2PositiveInt(r.URL.Query().Get("limit"), 200)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_limit", err.Error())
		return
	}
	offset, err := stockV2NonNegativeInt(r.URL.Query().Get("offset"), 0)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_offset", err.Error())
		return
	}
	filter := stockv2.OpportunityMarketScanCandidateListFilter{ScanRunID: r.PathValue("id"), Stage: strings.TrimSpace(r.URL.Query().Get("stage")), Limit: limit, Offset: offset}
	items, err := s.stockV2.ListOpportunityMarketScanCandidates(r.Context(), filter)
	if err != nil {
		writeOpportunityMarketScanError(w, err)
		return
	}
	total, err := s.stockV2.CountOpportunityMarketScanCandidates(r.Context(), filter)
	if err != nil {
		writeOpportunityMarketScanError(w, err)
		return
	}
	s.writeJSON(w, map[string]any{"items": items, "total": total, "limit": limit, "offset": offset})
}

func (s *Server) handleStockV2RetryOpportunityMarketScanRun(w http.ResponseWriter, r *http.Request) {
	session, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, session.Session) {
		return
	}
	run, err := s.stockV2.RetryOpportunityMarketScanRun(r.Context(), r.PathValue("id"), session.Session.OwnerID)
	if err != nil {
		writeOpportunityMarketScanError(w, err)
		return
	}
	auditOpportunityMarketScan(r, s, session.Session.OwnerID, "retried", run)
	s.writeJSON(w, run)
}

func auditOpportunityMarketScan(r *http.Request, s *Server, ownerID, action string, run stockv2.OpportunityMarketScanRun) {
	_, _ = s.store.AddAudit(r.Context(), storage.AuditEvent{EventType: "stockv2_opportunity_market_scan_" + action, RiskLevel: "medium", Summary: "执行机会发现市场扫描", Payload: map[string]any{"ownerId": ownerID, "runId": run.ID, "status": run.Status, "tradeDate": run.TradeDate}})
}

func writeOpportunityMarketScanError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	code := "opportunity_market_scan_failed"
	switch {
	case errors.Is(err, stockv2.ErrOpportunityMarketScanRunNotFound):
		status, code = http.StatusNotFound, "opportunity_market_scan_not_found"
	case errors.Is(err, stockv2.ErrOpportunityMarketScanAlreadyRunning):
		status, code = http.StatusConflict, "opportunity_market_scan_running"
	case errors.Is(err, stockv2.ErrOpportunityMarketScanDataNotReady), errors.Is(err, stockv2.ErrOpportunityMarketScanInvalidState):
		status, code = http.StatusConflict, "opportunity_market_scan_not_ready"
	}
	writeError(w, status, code, err.Error())
}
