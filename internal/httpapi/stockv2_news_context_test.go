package httpapi

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"phantom-lancer/internal/stockv2"
)

func TestStockV2NewsContextFilters(t *testing.T) {
	t.Run("valid theme filter", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/stockv2/news-context/themes?status=active&stage=accelerating&reviewStatus=completed&indexStatus=ready&q=AI&affected=semiconductor&since=2026-07-01&until=2026-07-12&limit=200&offset=3", nil)
		filter, err := stockV2NewsThreadFilterFromRequest(req)
		if err != nil {
			t.Fatalf("parse theme filter: %v", err)
		}
		if filter.Status != stockv2.NewsThreadStatusActive || filter.Stage != stockv2.NewsThreadStageAccelerating || filter.Limit != 200 || filter.Offset != 3 || filter.Since.IsZero() || filter.Until.IsZero() {
			t.Fatalf("unexpected theme filter: %+v", filter)
		}
	})

	tests := []struct {
		name   string
		target string
		parse  func(*http.Request) error
	}{
		{name: "theme status", target: "/?status=unknown", parse: parseNewsThreadFilterForTest},
		{name: "theme stage", target: "/?stage=unknown", parse: parseNewsThreadFilterForTest},
		{name: "theme review", target: "/?reviewStatus=unknown", parse: parseNewsThreadFilterForTest},
		{name: "theme index", target: "/?indexStatus=unknown", parse: parseNewsThreadFilterForTest},
		{name: "page too large", target: "/?limit=201", parse: parseNewsThreadFilterForTest},
		{name: "zero page", target: "/?limit=0", parse: parseNewsThreadFilterForTest},
		{name: "negative offset", target: "/?offset=-1", parse: parseNewsThreadFilterForTest},
		{name: "invalid since", target: "/?since=yesterday", parse: parseNewsThreadFilterForTest},
		{name: "reversed time window", target: "/?since=2026-07-12&until=2026-07-01", parse: parseNewsThreadFilterForTest},
		{name: "aggregation window", target: "/?windowType=weekly", parse: parseNewsContextRunFilterForTest},
		{name: "aggregation trigger", target: "/?triggerType=unknown", parse: parseNewsContextRunFilterForTest},
		{name: "aggregation status", target: "/?status=partial", parse: parseNewsContextRunFilterForTest},
		{name: "aggregation review", target: "/?reviewStatus=unknown", parse: parseNewsContextRunFilterForTest},
		{name: "cleanup status", target: "/?status=waiting_review", parse: parseNewsContextCleanupFilterForTest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.target, nil)
			if err := tt.parse(req); err == nil {
				t.Fatal("expected invalid filter")
			}
		})
	}
}

func TestStockV2NewsContextRunRequestValidation(t *testing.T) {
	for _, req := range []stockv2.RequestStartNewsContextRun{
		{WindowType: "weekly"},
		{WindowType: stockv2.NewsContextWindowHourly, StartAt: "yesterday"},
		{WindowType: stockv2.NewsContextWindowHourly, StartAt: "2026-07-12", EndAt: "2026-07-01"},
	} {
		if err := stockV2ValidateNewsContextRunRequest(req); err == nil {
			t.Fatalf("request=%+v, want validation error", req)
		}
	}
	if err := stockV2ValidateNewsContextRunRequest(stockv2.RequestStartNewsContextRun{WindowType: stockv2.NewsContextWindowHourly}); err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}
}

func TestStockV2NewsContextPOSTRequiresAuthAndCSRF(t *testing.T) {
	server, _, session, csrf := newStockV2HTTPTest(t)
	mux := http.NewServeMux()
	server.RegisterStockV2Routes(mux)

	paths := []string{
		"/api/stockv2/news-context/runs",
		"/api/stockv2/news-context/runs/run-1/retry",
		"/api/stockv2/news-context/cleanup-runs",
		"/api/stockv2/news-context/backfill",
		"/api/stockv2/news-context/backfill/pause",
		"/api/stockv2/news-context/backfill/resume",
		"/api/stockv2/news-context/backfill/retry",
	}
	for _, path := range paths {
		t.Run(path+" unauthenticated", func(t *testing.T) {
			rec := serveStockV2NewsContextRequest(mux, path, nil, "", "")
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status=%d body=%s, want 401", rec.Code, rec.Body.String())
			}
		})
		t.Run(path+" csrf", func(t *testing.T) {
			rec := serveStockV2NewsContextRequest(mux, path, nil, session, "")
			if rec.Code != http.StatusForbidden {
				t.Fatalf("status=%d body=%s, want 403", rec.Code, rec.Body.String())
			}
		})
		t.Run(path+" authenticated", func(t *testing.T) {
			rec := serveStockV2NewsContextRequest(mux, path, nil, session, csrf)
			if rec.Code == http.StatusUnauthorized || rec.Code == http.StatusForbidden {
				t.Fatalf("status=%d body=%s, authenticated request was rejected", rec.Code, rec.Body.String())
			}
		})
	}
	for _, tt := range []struct {
		name    string
		session string
		csrf    string
		want    int
	}{
		{name: "config unauthenticated", want: http.StatusUnauthorized},
		{name: "config csrf", session: session, want: http.StatusForbidden},
	} {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPatch, "/api/stockv2/news-context/config", strings.NewReader(`{"enabled":true}`))
			if tt.session != "" {
				req.AddCookie(&http.Cookie{Name: sessionCookie, Value: tt.session})
			}
			if tt.csrf != "" {
				req.Header.Set("X-CSRF-Token", tt.csrf)
			}
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != tt.want {
				t.Fatalf("status=%d body=%s, want %d", rec.Code, rec.Body.String(), tt.want)
			}
		})
	}

	t.Run("removed batch setting is rejected", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPatch, "/api/stockv2/news-context/config", strings.NewReader(`{"batchSize":25}`))
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: session})
		req.Header.Set("X-CSRF-Token", csrf)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s, want 400", rec.Code, rec.Body.String())
		}
	})

	for _, tt := range []struct {
		name string
		path string
		body string
	}{
		{name: "aggregation malformed body", path: "/api/stockv2/news-context/runs", body: "{"},
		{name: "aggregation trailing body", path: "/api/stockv2/news-context/runs", body: `{"windowType":"hourly"} {}`},
		{name: "cleanup malformed body", path: "/api/stockv2/news-context/cleanup-runs", body: "{"},
		{name: "cleanup unknown field", path: "/api/stockv2/news-context/cleanup-runs", body: `{"befroe":"2026-07-01"}`},
		{name: "cleanup invalid time", path: "/api/stockv2/news-context/cleanup-runs", body: `{"before":"yesterday"}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rec := serveStockV2NewsContextRequest(mux, tt.path, []byte(tt.body), session, csrf)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s, want 400", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestStockV2NewsContextPrivateGETRequiresAuth(t *testing.T) {
	server, _, session, _ := newStockV2HTTPTest(t)
	mux := http.NewServeMux()
	server.RegisterStockV2Routes(mux)

	for _, path := range []string{
		"/api/stockv2/news-context/config",
		"/api/stockv2/news-context/summary",
		"/api/stockv2/news-context/themes",
		"/api/stockv2/news-context/themes/missing-theme",
		"/api/stockv2/news-context/rotation-signals",
		"/api/stockv2/news-context/runs",
	} {
		t.Run(path+" unauthenticated", func(t *testing.T) {
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status=%d body=%s, want 401", rec.Code, rec.Body.String())
			}
		})
		t.Run(path+" authenticated", func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.AddCookie(&http.Cookie{Name: sessionCookie, Value: session})
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code == http.StatusUnauthorized || rec.Code == http.StatusForbidden {
				t.Fatalf("status=%d body=%s, authenticated request was rejected", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestStockV2NewsContextBackfillGETRequiresAuth(t *testing.T) {
	server, _, session, _ := newStockV2HTTPTest(t)
	mux := http.NewServeMux()
	server.RegisterStockV2Routes(mux)
	for _, path := range []string{
		"/api/stockv2/news-context/backfill/preview",
		"/api/stockv2/news-context/backfill",
	} {
		t.Run(path+" unauthenticated", func(t *testing.T) {
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status=%d body=%s, want 401", rec.Code, rec.Body.String())
			}
		})
		t.Run(path+" authenticated", func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.AddCookie(&http.Cookie{Name: sessionCookie, Value: session})
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code == http.StatusUnauthorized || rec.Code == http.StatusForbidden {
				t.Fatalf("status=%d body=%s, authenticated request was rejected", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestStockV2NewsContextInvalidQueryRoutes(t *testing.T) {
	server, _, session, _ := newStockV2HTTPTest(t)
	mux := http.NewServeMux()
	server.RegisterStockV2Routes(mux)
	for _, target := range []string{
		"/api/stockv2/news-context/themes?limit=201",
		"/api/stockv2/news-context/runs?kind=unknown",
		"/api/stockv2/news-context/runs?kind=aggregation&windowType=weekly",
		"/api/stockv2/news-context/runs?kind=cleanup&status=waiting_review",
	} {
		t.Run(target, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, target, nil)
			req.AddCookie(&http.Cookie{Name: sessionCookie, Value: session})
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s, want 400", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestStockV2NewsContextHTTPStatus(t *testing.T) {
	for _, tt := range []struct {
		err  error
		want int
	}{
		{err: stockv2.ErrInvalidNewsContextInput, want: http.StatusBadRequest},
		{err: stockv2.ErrNewsThreadNotFound, want: http.StatusNotFound},
		{err: stockv2.ErrNewsContextAlreadyRunning, want: http.StatusConflict},
		{err: stockv2.ErrNewsContextPrerequisite, want: http.StatusConflict},
		{err: errors.New("storage failed"), want: http.StatusInternalServerError},
	} {
		if got := stockV2NewsContextHTTPStatus(tt.err); got != tt.want {
			t.Fatalf("status(%v)=%d, want %d", tt.err, got, tt.want)
		}
	}
	rec := httptest.NewRecorder()
	writeStockV2NewsContextError(rec, "failed", errors.New("storage /private/path failed"))
	if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "/private/path") {
		t.Fatalf("internal response status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func parseNewsThreadFilterForTest(r *http.Request) error {
	_, err := stockV2NewsThreadFilterFromRequest(r)
	return err
}

func parseNewsContextRunFilterForTest(r *http.Request) error {
	_, err := stockV2NewsContextRunFilterFromRequest(r)
	return err
}

func parseNewsContextCleanupFilterForTest(r *http.Request) error {
	_, err := stockV2NewsContextCleanupRunFilterFromRequest(r)
	return err
}

func serveStockV2NewsContextRequest(handler http.Handler, path string, body []byte, session, csrf string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	if session != "" {
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: session})
	}
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}
