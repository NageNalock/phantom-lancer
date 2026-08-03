package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"phantom-lancer/internal/stockv2"
)

func (s *Server) handleStockV2ListNewsSources(w http.ResponseWriter, r *http.Request) {
	items, err := s.stockV2.ListNewsSourceOverviews(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.writeJSON(w, map[string]any{"items": items})
}

func (s *Server) handleStockV2UpdateNewsSourceConfig(w http.ResponseWriter, r *http.Request) {
	session, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, session.Session) {
		return
	}
	var req stockv2.NewsSourceConfigPatch
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	item, err := s.stockV2.UpdateNewsSourceConfig(r.Context(), r.PathValue("source"), req)
	if err != nil {
		http.Error(w, err.Error(), stockV2NewsHTTPStatus(err))
		return
	}
	s.writeJSON(w, item)
}

func (s *Server) handleStockV2RunNewsSourceOnce(w http.ResponseWriter, r *http.Request) {
	session, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, session.Session) {
		return
	}
	result, err := s.stockV2.RunNewsPipelineOnce(r.Context(), r.PathValue("source"))
	if err != nil {
		writeError(w, stockV2NewsHTTPStatus(err), "stockv2_news_source_failed", err.Error())
		return
	}
	s.writeJSON(w, result)
}

func (s *Server) handleStockV2ListRawNews(w http.ResponseWriter, r *http.Request) {
	filter := rawNewsFilterFromRequest(r)
	items, err := s.stockV2.ListRawNews(r.Context(), filter)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	total, err := s.stockV2.CountRawNews(r.Context(), filter)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.writeJSON(w, map[string]any{"items": items, "total": total, "limit": filter.Limit, "offset": filter.Offset})
}

func (s *Server) handleStockV2GetRawNews(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "id is required", http.StatusBadRequest)
		return
	}
	item, err := s.stockV2.GetRawNews(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), stockV2NewsHTTPStatus(err))
		return
	}
	s.writeJSON(w, item)
}

func (s *Server) handleStockV2TruncateRawNews(w http.ResponseWriter, r *http.Request) {
	session, ok := s.requireAuth(w, r)
	if !ok || !s.requireCSRF(w, r, session.Session) {
		return
	}
	var req struct {
		Before string `json:"before"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	before := parseStockV2NewsTime(req.Before)
	if before.IsZero() {
		writeError(w, http.StatusBadRequest, "invalid_before", stockv2.ErrInvalidRawNewsTruncateBefore.Error())
		return
	}
	result, err := s.stockV2.TruncateRawNewsBefore(r.Context(), before)
	if err != nil {
		writeError(w, stockV2NewsHTTPStatus(err), "stockv2_raw_news_truncate_failed", err.Error())
		return
	}
	s.writeJSON(w, result)
}

func (s *Server) handleStockV2ListNewsEvents(w http.ResponseWriter, r *http.Request) {
	filter := newsEventFilterFromRequest(r)
	items, err := s.stockV2.ListNewsEvents(r.Context(), filter)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	total, err := s.stockV2.CountNewsEvents(r.Context(), filter)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.writeJSON(w, map[string]any{"items": items, "total": total, "limit": filter.Limit, "offset": filter.Offset})
}

func (s *Server) handleStockV2ListNewsLinkCandidates(w http.ResponseWriter, r *http.Request) {
	filter := newsLinkCandidateFilterFromRequest(r)
	items, err := s.stockV2.ListNewsLinkCandidates(r.Context(), filter)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	total, err := s.stockV2.CountNewsLinkCandidates(r.Context(), filter)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.writeJSON(w, map[string]any{"items": items, "total": total, "limit": filter.Limit, "offset": filter.Offset})
}

func rawNewsFilterFromRequest(r *http.Request) stockv2.RawNewsListFilter {
	query := r.URL.Query()
	return stockv2.RawNewsListFilter{
		Source:   query.Get("source"),
		Language: query.Get("language"),
		Status:   query.Get("status"),
		Quality:  query.Get("quality"),
		Query:    query.Get("q"),
		Since:    parseStockV2NewsTime(query.Get("since")),
		Until:    parseStockV2NewsTime(query.Get("until")),
		Limit:    stockV2NewsInt(query.Get("limit"), 50, 200),
		Offset:   stockV2NewsOffset(query.Get("offset")),
	}
}

func newsEventFilterFromRequest(r *http.Request) stockv2.NewsEventListFilter {
	query := r.URL.Query()
	return stockv2.NewsEventListFilter{
		Source:        query.Get("source"),
		LinkStatus:    query.Get("linkStatus"),
		QualityStatus: query.Get("qualityStatus"),
		Query:         query.Get("q"),
		Since:         parseStockV2NewsTime(query.Get("since")),
		Until:         parseStockV2NewsTime(query.Get("until")),
		Limit:         stockV2NewsInt(query.Get("limit"), 50, 200),
		Offset:        stockV2NewsOffset(query.Get("offset")),
	}
}

func newsLinkCandidateFilterFromRequest(r *http.Request) stockv2.NewsLinkCandidateListFilter {
	query := r.URL.Query()
	return stockv2.NewsLinkCandidateListFilter{
		NewsEventID: query.Get("newsEventId"),
		RawNewsID:   query.Get("rawNewsId"),
		Source:      query.Get("source"),
		Symbol:      query.Get("symbol"),
		Market:      query.Get("market"),
		MatchMethod: query.Get("matchMethod"),
		Query:       query.Get("q"),
		Since:       parseStockV2NewsTime(query.Get("since")),
		Until:       parseStockV2NewsTime(query.Get("until")),
		Limit:       stockV2NewsInt(query.Get("limit"), 50, 200),
		Offset:      stockV2NewsOffset(query.Get("offset")),
	}
}

func stockV2NewsInt(raw string, fallback int, max int) int {
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return fallback
	}
	if value > max {
		return max
	}
	return value
}

func stockV2NewsOffset(raw string) int {
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return 0
	}
	return value
}

func parseStockV2NewsTime(raw string) time.Time {
	if raw == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02"} {
		if ts, err := time.Parse(layout, raw); err == nil {
			return ts
		}
	}
	for _, layout := range []string{"2006-01-02T15:04", "2006-01-02T15:04:05", "2006-01-02 15:04", "2006-01-02 15:04:05"} {
		if ts, err := time.ParseInLocation(layout, raw, time.Local); err == nil {
			return ts
		}
	}
	return time.Time{}
}

func stockV2NewsHTTPStatus(err error) int {
	switch {
	case errors.Is(err, stockv2.ErrNewsAdapterDisabled),
		errors.Is(err, stockv2.ErrUnsupportedNewsSource),
		errors.Is(err, stockv2.ErrFinancialJuiceCookieMissing),
		errors.Is(err, stockv2.ErrFinancialJuiceInvalidCredential),
		errors.Is(err, stockv2.ErrNewsSourceAdapterNotFound),
		errors.Is(err, stockv2.ErrNewsSourceCredentialUnsupported),
		errors.Is(err, stockv2.ErrInvalidRawNewsTruncateBefore):
		return http.StatusBadRequest
	case errors.Is(err, stockv2.ErrRawNewsNotFound):
		return http.StatusNotFound
	default:
		return http.StatusBadGateway
	}
}
