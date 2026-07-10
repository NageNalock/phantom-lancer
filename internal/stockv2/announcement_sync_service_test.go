package stockv2

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
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
	if len(forms) < 3 || forms[2].Get("seDate") != "2026-07-09~2026-07-11" {
		got := ""
		if len(forms) >= 3 {
			got = forms[2].Get("seDate")
		}
		mu.Unlock()
		t.Fatalf("overlap seDate=%q forms=%d", got, len(forms))
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

func writeCninfoSyncPage(t *testing.T, w http.ResponseWriter, total int, announcements []map[string]any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
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
