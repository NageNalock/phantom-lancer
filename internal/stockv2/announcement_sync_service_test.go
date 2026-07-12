package stockv2

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSyncAnnouncementMarketsPaginatesDedupesAndRecoversFromFailure(t *testing.T) {
	var mu sync.Mutex
	failedBatch := false
	forms := make([]url.Values, 0, 8)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		mu.Lock()
		forms = append(forms, cloneURLValues(r.Form))
		fail := failedBatch
		mu.Unlock()
		dateRange := strings.Split(r.Form.Get("seDate"), "~")
		if len(dateRange) == 2 && dateRange[0] == dateRange[1] && dateRange[0] < "2026-07-09" {
			writeCninfoSyncPage(t, w, 0, nil)
			return
		}
		page, _ := strconv.Atoi(r.Form.Get("pageNum"))
		if fail && page == 2 {
			http.Error(w, "temporary failure", http.StatusServiceUnavailable)
			return
		}
		if fail {
			writeCninfoSyncPage(t, w, 3, []map[string]any{
				cninfoSyncTestAnnouncement("000002", "new-1", "失败批次新公告一", "2026-07-12 08:00:00"),
				cninfoSyncTestAnnouncement("000003", "new-2", "失败批次新公告二", "2026-07-12 09:00:00"),
			})
			return
		}
		switch page {
		case 1:
			writeCninfoSyncPage(t, w, 3, []map[string]any{
				cninfoSyncTestAnnouncement("000001", "a-1", "公告一", "2026-07-10 00:30:00"),
				cninfoSyncTestAnnouncement("000002", "a-2", "公告二", "2026-07-10 01:00:00"),
			})
		case 2:
			writeCninfoSyncPage(t, w, 3, []map[string]any{
				cninfoSyncTestAnnouncement("000001", "a-3", "重大合同公告", "2026-07-10 01:30:00"),
			})
		default:
			writeCninfoSyncPage(t, w, 3, nil)
		}
	}))
	defer server.Close()

	dir := t.TempDir()
	store, err := NewStoreWithMarketDB(filepath.Join(dir, "stockv2.sqlite"), filepath.Join(dir, "stock_market.duckdb"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer store.Close()
	svc := NewService(store, nil, server.Client())
	svc.announcementSource.queryURL = server.URL
	ctx := context.Background()
	firstNow := time.Date(2026, 7, 10, 2, 0, 0, 0, time.Local)
	req := AnnouncementMarketsSyncRequest{
		Markets:         []string{"SZ"},
		PageSize:        2,
		MaxPages:        3,
		Overlap:         6 * time.Hour,
		InitialLookback: 24 * time.Hour,
		Now:             firstNow,
	}
	first, err := svc.SyncAnnouncementMarkets(ctx, req)
	if err != nil {
		t.Fatalf("first sync: %v", err)
	}
	if len(first.Markets) != 1 || first.Markets[0].PagesFetched != 2 || first.Markets[0].FetchedCount != 3 || first.Markets[0].InsertedCount != 3 {
		t.Fatalf("unexpected first result: %+v", first)
	}
	if len(first.NewBySymbol["000001"]) != 2 || len(first.NewBySymbol["000002"]) != 1 {
		t.Fatalf("unexpected symbol distribution: %+v", first.NewBySymbol)
	}
	state, ok, err := store.GetAnnouncementSyncState(ctx, StockV2AnnouncementSourceCninfo, "SZ")
	if err != nil || !ok {
		t.Fatalf("get first state ok=%v err=%v", ok, err)
	}
	if !state.CoveredThrough.Equal(firstNow) || state.LastFetchedCount != 3 || state.LastInsertedCount != 3 {
		t.Fatalf("unexpected first state: %+v", state)
	}

	secondNow := time.Date(2026, 7, 11, 5, 0, 0, 0, time.Local)
	req.Now = secondNow
	second, err := svc.SyncAnnouncementMarkets(ctx, req)
	if err != nil {
		t.Fatalf("second sync: %v", err)
	}
	if second.Markets[0].InsertedCount != 0 || len(second.NewBySymbol) != 0 {
		t.Fatalf("duplicates should not be new: %+v", second)
	}
	state, ok, err = store.GetAnnouncementSyncState(ctx, StockV2AnnouncementSourceCninfo, "SZ")
	if err != nil || !ok || !state.CoveredThrough.Equal(secondNow) || state.LastInsertedCount != 0 {
		t.Fatalf("unexpected second state ok=%v state=%+v err=%v", ok, state, err)
	}
	mu.Lock()
	foundOverlap := false
	for _, form := range forms {
		if form.Get("seDate") == "2026-07-09~2026-07-11" {
			foundOverlap = true
			break
		}
	}
	if !foundOverlap {
		got := make([]string, 0, len(forms))
		for _, form := range forms {
			got = append(got, form.Get("seDate"))
		}
		mu.Unlock()
		t.Fatalf("overlap seDate missing; got=%v", got)
	}
	failedBatch = true
	mu.Unlock()

	beforeCount, err := store.CountAnnouncements(ctx, AnnouncementListFilter{})
	if err != nil {
		t.Fatalf("count before failed batch: %v", err)
	}
	req.Now = time.Date(2026, 7, 12, 12, 0, 0, 0, time.Local)
	if _, err := svc.SyncAnnouncementMarkets(ctx, req); err == nil {
		t.Fatal("expected page failure")
	}
	afterState, ok, err := store.GetAnnouncementSyncState(ctx, StockV2AnnouncementSourceCninfo, "SZ")
	if err != nil || !ok {
		t.Fatalf("get state after failure ok=%v err=%v", ok, err)
	}
	if !afterState.CoveredThrough.Equal(secondNow) {
		t.Fatalf("cursor advanced after failed page: before=%v after=%v", secondNow, afterState.CoveredThrough)
	}
	afterCount, err := store.CountAnnouncements(ctx, AnnouncementListFilter{})
	if err != nil {
		t.Fatalf("count after failed batch: %v", err)
	}
	if afterCount != beforeCount {
		t.Fatalf("failed batch persisted partial announcements: before=%d after=%d", beforeCount, afterCount)
	}
}

func TestSyncAnnouncementMarketsFindsDelayedPublicationThroughRollingDateBucket(t *testing.T) {
	var mu sync.Mutex
	requestedDates := make([]string, 0, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		dateRange := r.Form.Get("seDate")
		mu.Lock()
		requestedDates = append(requestedDates, dateRange)
		mu.Unlock()
		if dateRange == "2026-06-13~2026-06-13" {
			writeCninfoSyncPage(t, w, 1, []map[string]any{
				cninfoSyncTestAnnouncement("000001", "late-1", "延迟入库的重大投资公告", "2026-06-13 10:00:00"),
			})
			return
		}
		writeCninfoSyncPage(t, w, 0, nil)
	}))
	defer server.Close()

	dir := t.TempDir()
	store, err := NewStoreWithMarketDB(filepath.Join(dir, "stockv2.sqlite"), filepath.Join(dir, "stock_market.duckdb"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer store.Close()
	svc := NewService(store, nil, server.Client())
	svc.announcementSource.queryURL = server.URL
	now := time.Date(2026, 7, 12, 12, 0, 0, 0, chinaMarketTZ)
	req := AnnouncementMarketsSyncRequest{Markets: []string{"SZ"}, Now: now}

	first, err := svc.SyncAnnouncementMarkets(context.Background(), req)
	if err != nil {
		t.Fatalf("first sync: %v", err)
	}
	if len(first.NewBySymbol) != 0 || first.Markets[0].LateRecheckDate.Format("2006-01-02") != "2026-06-12" {
		t.Fatalf("first sync = %+v", first)
	}
	sameDay, err := svc.SyncAnnouncementMarkets(context.Background(), req)
	if err != nil {
		t.Fatalf("same-day sync: %v", err)
	}
	if !sameDay.Markets[0].LateRecheckDate.IsZero() {
		t.Fatalf("same-day sync advanced another historical bucket: %+v", sameDay.Markets[0])
	}
	req.Now = now.AddDate(0, 0, 1)
	second, err := svc.SyncAnnouncementMarkets(context.Background(), req)
	if err != nil {
		t.Fatalf("second sync: %v", err)
	}
	delayed := second.NewBySymbol["000001"]
	if len(delayed) != 1 || delayed[0].AnnouncementID != "late-1" || !delayed[0].Major {
		t.Fatalf("delayed announcements = %+v", delayed)
	}
	if !delayed[0].PublishedAt.Before(now.Add(-6 * time.Hour)) {
		t.Fatalf("test announcement is not older than overlap: %v", delayed[0].PublishedAt)
	}
	state, ok, err := store.GetAnnouncementSyncState(context.Background(), StockV2AnnouncementSourceCninfo, "SZ")
	if err != nil || !ok || announcementShanghaiDay(state.LateRecheckCoveredThrough).Format("2006-01-02") != "2026-06-13" {
		t.Fatalf("late recheck state = %+v, ok=%v, err=%v", state, ok, err)
	}
	mu.Lock()
	dates := append([]string(nil), requestedDates...)
	mu.Unlock()
	if !announcementSyncContainsString(dates, "2026-06-12~2026-06-12") || !announcementSyncContainsString(dates, "2026-06-13~2026-06-13") {
		t.Fatalf("historical date buckets = %v", dates)
	}
}

func TestSyncAnnouncementMarketsDoesNotAdvanceOnUnknownHTTP200Envelope(t *testing.T) {
	unknown := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if unknown {
			_, _ = w.Write([]byte(`{"data":[]}`))
			return
		}
		writeCninfoSyncPage(t, w, 0, nil)
	}))
	defer server.Close()
	dir := t.TempDir()
	store, err := NewStoreWithMarketDB(filepath.Join(dir, "stockv2.sqlite"), filepath.Join(dir, "stock_market.duckdb"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer store.Close()
	svc := NewService(store, nil, server.Client())
	svc.announcementSource.queryURL = server.URL
	ctx := context.Background()
	now := time.Date(2026, 7, 12, 12, 0, 0, 0, chinaMarketTZ)
	if _, err := svc.SyncAnnouncementMarkets(ctx, AnnouncementMarketsSyncRequest{Markets: []string{"SZ"}, Now: now}); err != nil {
		t.Fatalf("initial sync: %v", err)
	}
	before, ok, err := store.GetAnnouncementSyncState(ctx, StockV2AnnouncementSourceCninfo, "SZ")
	if err != nil || !ok {
		t.Fatalf("initial state = %+v, ok=%v, err=%v", before, ok, err)
	}
	unknown = true
	if _, err := svc.SyncAnnouncementMarkets(ctx, AnnouncementMarketsSyncRequest{Markets: []string{"SZ"}, Now: now.Add(time.Hour)}); err == nil {
		t.Fatal("unknown HTTP 200 envelope was accepted")
	}
	after, ok, err := store.GetAnnouncementSyncState(ctx, StockV2AnnouncementSourceCninfo, "SZ")
	if err != nil || !ok {
		t.Fatalf("state after failure = %+v, ok=%v, err=%v", after, ok, err)
	}
	if !after.CoveredThrough.Equal(before.CoveredThrough) ||
		!after.LateRecheckCoveredThrough.Equal(before.LateRecheckCoveredThrough) {
		t.Fatalf("cursor advanced: before=%+v after=%+v", before, after)
	}
}

func TestValidateAnnouncementSyncStateRejectsFutureWatermarks(t *testing.T) {
	now := time.Date(2026, 7, 12, 12, 0, 0, 0, chinaMarketTZ)
	base := AnnouncementSyncState{
		CoveredThrough:            now,
		LatestPublishedAt:         now.Add(-time.Hour),
		LastSuccessAt:             now,
		LastWindowStart:           now.Add(-6 * time.Hour),
		LastWindowEnd:             now,
		LateRecheckStartedAt:      now.AddDate(0, 0, -30),
		LateRecheckCoveredThrough: now.AddDate(0, 0, -1),
		LastLateRecheckAt:         now,
	}
	mutations := map[string]func(*AnnouncementSyncState){
		"covered": func(state *AnnouncementSyncState) {
			state.CoveredThrough = now.Add(announcementSyncClockSkew + time.Second)
		},
		"last success": func(state *AnnouncementSyncState) {
			state.LastSuccessAt = now.Add(announcementSyncClockSkew + time.Second)
		},
		"late checked": func(state *AnnouncementSyncState) {
			state.LastLateRecheckAt = now.Add(announcementSyncClockSkew + time.Second)
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			state := base
			mutate(&state)
			if err := validateAnnouncementSyncStateAt(state, true, now); err == nil {
				t.Fatal("future announcement watermark was accepted")
			}
		})
	}
}

func announcementSyncContainsString(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}

func writeCninfoSyncPage(t *testing.T, w http.ResponseWriter, total int, announcements []map[string]any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if announcements == nil {
		announcements = []map[string]any{}
	}
	if err := json.NewEncoder(w).Encode(map[string]any{"totalAnnouncement": total, "announcements": announcements}); err != nil {
		t.Errorf("encode response: %v", err)
	}
}

func cninfoSyncTestAnnouncement(symbol, id, title, publishedAt string) map[string]any {
	return map[string]any{
		"secCode":           symbol,
		"orgId":             "org-" + symbol,
		"announcementId":    id,
		"announcementTitle": title,
		"adjunctUrl":        "finalpage/" + id + ".pdf",
		"announcementTime":  publishedAt,
	}
}

func cloneURLValues(values url.Values) url.Values {
	cloned := make(url.Values, len(values))
	for key, items := range values {
		cloned[key] = append([]string(nil), items...)
	}
	return cloned
}
