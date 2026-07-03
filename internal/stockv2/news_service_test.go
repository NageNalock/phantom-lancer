package stockv2

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRawNewsCreateDedupeAndPagination(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()

	first, err := svc.CreateRawNews(ctx, RequestCreateRawNews{
		Source:      "jin10",
		SourceID:    "flash-1",
		Language:    "zh-CN",
		Title:       "算力产业链出现新进展",
		Snippet:     "服务器产业链相关公司受到关注。",
		PublishedAt: time.Now().Add(-time.Minute),
		RawPayload:  map[string]any{"kind": "flash"},
	})
	if err != nil {
		t.Fatalf("create raw news: %v", err)
	}
	duplicate, err := svc.CreateRawNews(ctx, RequestCreateRawNews{
		Source:   "jin10",
		SourceID: "flash-1",
		Title:    "重复快讯标题不同也应按 source_id 去重",
	})
	if err != nil {
		t.Fatalf("create duplicate raw news: %v", err)
	}
	if duplicate.ID != first.ID {
		t.Fatalf("duplicate id = %q, want %q", duplicate.ID, first.ID)
	}

	for _, sourceID := range []string{"flash-2", "flash-3"} {
		if _, err := svc.CreateRawNews(ctx, RequestCreateRawNews{
			Source:   "jin10",
			SourceID: sourceID,
			Title:    "分页测试消息",
		}); err != nil {
			t.Fatalf("create raw news %s: %v", sourceID, err)
		}
	}
	total, err := svc.CountRawNews(ctx, RawNewsListFilter{Source: "jin10"})
	if err != nil {
		t.Fatalf("count raw news: %v", err)
	}
	if total != 3 {
		t.Fatalf("raw news count = %d, want 3", total)
	}
	page, err := svc.ListRawNews(ctx, RawNewsListFilter{Source: "jin10", Limit: 2, Offset: 1})
	if err != nil {
		t.Fatalf("list raw news: %v", err)
	}
	if len(page) != 2 {
		t.Fatalf("page len = %d, want 2", len(page))
	}
	for _, item := range page {
		if item.RawPayload != nil {
			t.Fatalf("list raw news leaked raw payload: %+v", item.RawPayload)
		}
	}
	detail, err := svc.GetRawNews(ctx, first.ID)
	if err != nil {
		t.Fatalf("get raw news detail: %v", err)
	}
	if detail.RawPayload["kind"] != "flash" {
		t.Fatalf("detail raw payload = %+v, want full payload", detail.RawPayload)
	}
	unprocessed, err := svc.ListUnprocessedRawNews(ctx, time.Now().Add(time.Hour), 10)
	if err != nil {
		t.Fatalf("list unprocessed raw news: %v", err)
	}
	if len(unprocessed) != 3 {
		t.Fatalf("unprocessed len = %d, want 3", len(unprocessed))
	}
}

func TestNewsInvalidInput(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()

	if _, err := svc.CreateRawNews(ctx, RequestCreateRawNews{Title: "missing source"}); !errors.Is(err, ErrInvalidRawNewsSource) {
		t.Fatalf("raw news err = %v, want ErrInvalidRawNewsSource", err)
	}
	if _, err := svc.CreateRawNews(ctx, RequestCreateRawNews{Source: "jin10"}); !errors.Is(err, ErrInvalidRawNewsContent) {
		t.Fatalf("raw news content err = %v, want ErrInvalidRawNewsContent", err)
	}
}

func TestRawNewsDuplicateFailedResetsToNew(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()

	failed, err := svc.CreateRawNews(ctx, RequestCreateRawNews{
		Source:   "financialjuice",
		SourceID: "9648580",
		Title:    "Iran's President Pezeshkian: We will never negotiate our defensive ability with anyone.",
		Status:   NewsStatusFailed,
	})
	if err != nil {
		t.Fatalf("create failed raw news: %v", err)
	}
	reloaded, err := svc.CreateRawNews(ctx, RequestCreateRawNews{
		Source:   "financialjuice",
		SourceID: "9648580",
		Title:    failed.Title,
		Status:   NewsStatusNew,
	})
	if err != nil {
		t.Fatalf("create duplicate raw news: %v", err)
	}
	if reloaded.ID != failed.ID || reloaded.Status != NewsStatusNew {
		t.Fatalf("reloaded = %+v, failed = %+v; want same row reset to new", reloaded, failed)
	}
}

func TestRawNewsListSortsByEffectiveTimeDesc(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	base := time.Date(2026, 6, 24, 9, 0, 0, 0, time.UTC)

	older, err := svc.CreateRawNews(ctx, RequestCreateRawNews{
		Source:      "jin10",
		SourceID:    "sort-old",
		Title:       "older published news",
		PublishedAt: base.Add(-time.Hour),
		FetchedAt:   base,
	})
	if err != nil {
		t.Fatalf("create older raw news: %v", err)
	}
	newer, err := svc.CreateRawNews(ctx, RequestCreateRawNews{
		Source:      "jin10",
		SourceID:    "sort-new",
		Title:       "newer published news",
		PublishedAt: base.Add(time.Hour),
		FetchedAt:   base.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("create newer raw news: %v", err)
	}
	future, err := svc.CreateRawNews(ctx, RequestCreateRawNews{
		Source:      "jin10",
		SourceID:    "sort-future",
		Title:       "future published news",
		PublishedAt: base.Add(8 * time.Hour),
		FetchedAt:   base.Add(-2 * time.Hour),
	})
	if err != nil {
		t.Fatalf("create future raw news: %v", err)
	}
	items, err := svc.ListRawNews(ctx, RawNewsListFilter{Source: "jin10", Limit: 10})
	if err != nil {
		t.Fatalf("list raw news: %v", err)
	}
	if len(items) < 3 || items[0].ID != newer.ID || items[1].ID != older.ID || items[2].ID != future.ID {
		t.Fatalf("items = %+v, want effective time desc", items)
	}
}

func TestCreateRawNewsClampsFuturePublishedAt(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	fetchedAt := time.Date(2026, 6, 24, 9, 0, 0, 0, time.UTC)

	item, err := svc.CreateRawNews(ctx, RequestCreateRawNews{
		Source:      "jin10",
		SourceID:    "future-clamp",
		Title:       "future raw news",
		PublishedAt: fetchedAt.Add(8 * time.Hour),
		FetchedAt:   fetchedAt,
	})
	if err != nil {
		t.Fatalf("create raw news: %v", err)
	}
	if !item.PublishedAt.Equal(fetchedAt) {
		t.Fatalf("published_at = %s, want fetched_at %s", item.PublishedAt, fetchedAt)
	}
}

func TestTruncateRawNewsBeforeDeletesOldEffectiveTime(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	base := time.Date(2026, 6, 24, 9, 0, 0, 0, time.UTC)
	if err := svc.store.UpsertNewsSourceState(ctx, NewsSourceState{Source: "jin10", Status: NewsSourceStatusIdle, RawNewsCount: 2}); err != nil {
		t.Fatalf("seed jin10 state: %v", err)
	}
	if err := svc.store.UpsertNewsSourceState(ctx, NewsSourceState{Source: "financialjuice", Status: NewsSourceStatusIdle, RawNewsCount: 1}); err != nil {
		t.Fatalf("seed financialjuice state: %v", err)
	}
	for _, req := range []RequestCreateRawNews{
		{Source: "jin10", SourceID: "truncate-old-published", Title: "old published", PublishedAt: base.Add(-2 * time.Hour), FetchedAt: base},
		{Source: "financialjuice", SourceID: "truncate-old-fetched", Title: "old fetched", FetchedAt: base.Add(-90 * time.Minute)},
		{Source: "jin10", SourceID: "truncate-keep", Title: "keep", PublishedAt: base.Add(-30 * time.Minute), FetchedAt: base.Add(-30 * time.Minute)},
	} {
		if _, err := svc.CreateRawNews(ctx, req); err != nil {
			t.Fatalf("create raw news %s: %v", req.SourceID, err)
		}
	}

	result, err := svc.TruncateRawNewsBefore(ctx, base.Add(-time.Hour))
	if err != nil {
		t.Fatalf("truncate raw news: %v", err)
	}
	if result.DeletedCount != 2 {
		t.Fatalf("deleted = %d, want 2", result.DeletedCount)
	}
	remaining, err := svc.ListRawNews(ctx, RawNewsListFilter{Limit: 10})
	if err != nil {
		t.Fatalf("list remaining raw news: %v", err)
	}
	if len(remaining) != 1 || remaining[0].SourceID != "truncate-keep" {
		t.Fatalf("remaining = %+v, want only truncate-keep", remaining)
	}
	jin10, ok, err := svc.GetNewsSourceState(ctx, "jin10")
	if err != nil || !ok {
		t.Fatalf("get jin10 state ok=%v err=%v", ok, err)
	}
	financialjuice, ok, err := svc.GetNewsSourceState(ctx, "financialjuice")
	if err != nil || !ok {
		t.Fatalf("get financialjuice state ok=%v err=%v", ok, err)
	}
	if jin10.RawNewsCount != 1 || financialjuice.RawNewsCount != 0 {
		t.Fatalf("raw counts jin10=%d financialjuice=%d, want 1/0", jin10.RawNewsCount, financialjuice.RawNewsCount)
	}
}

func TestPruneRawNewsRetentionProcessesAndDeletesOlderThanWindow(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Date(2026, 7, 4, 10, 0, 0, 0, time.UTC)
	if err := svc.store.UpsertNewsSourceState(ctx, NewsSourceState{Source: "jin10", Status: NewsSourceStatusIdle, RawNewsCount: 3}); err != nil {
		t.Fatalf("seed state: %v", err)
	}
	for _, req := range []RequestCreateRawNews{
		{Source: "jin10", SourceID: "retention-old-new", Title: "旧消息先转事件", PublishedAt: now.Add(-5 * time.Hour), FetchedAt: now.Add(-5 * time.Hour)},
		{Source: "jin10", SourceID: "retention-old-failed", Title: "旧失败消息", PublishedAt: now.Add(-6 * time.Hour), FetchedAt: now.Add(-6 * time.Hour), Status: NewsStatusFailed},
		{Source: "jin10", SourceID: "retention-recent", Title: "近期消息保留", PublishedAt: now.Add(-2 * time.Hour), FetchedAt: now.Add(-2 * time.Hour)},
	} {
		if _, err := svc.CreateRawNews(ctx, req); err != nil {
			t.Fatalf("create raw news %s: %v", req.SourceID, err)
		}
	}

	result, err := svc.PruneRawNewsRetention(ctx, now)
	if err != nil {
		t.Fatalf("prune raw news retention: %v", err)
	}
	if result.DeletedCount != 2 || result.ProcessedBeforePrune != 2 {
		t.Fatalf("retention result = %#v, want delete 2 process 2", result)
	}
	remaining, err := svc.ListRawNews(ctx, RawNewsListFilter{Limit: 10})
	if err != nil {
		t.Fatalf("list raw news: %v", err)
	}
	if len(remaining) != 1 || remaining[0].SourceID != "retention-recent" {
		t.Fatalf("remaining raw news = %+v, want recent only", remaining)
	}
	events, err := svc.ListNewsEvents(ctx, NewsEventListFilter{Source: "jin10", Query: "旧消息先转事件", Limit: 10})
	if err != nil {
		t.Fatalf("list news events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events len = %d, want processed old event", len(events))
	}
}

func TestNewsEventDedupeSurvivesRawNewsRetention(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Date(2026, 7, 4, 10, 0, 0, 0, time.UTC)
	if err := svc.store.UpsertNewsSourceState(ctx, NewsSourceState{Source: "jin10", Status: NewsSourceStatusIdle, RawNewsCount: 1}); err != nil {
		t.Fatalf("seed state: %v", err)
	}
	req := RequestCreateRawNews{
		Source:      "jin10",
		SourceID:    "retention-dedupe",
		Title:       "重复旧消息",
		PublishedAt: now.Add(-5 * time.Hour),
		FetchedAt:   now.Add(-5 * time.Hour),
	}
	if _, err := svc.CreateRawNews(ctx, req); err != nil {
		t.Fatalf("create raw news: %v", err)
	}
	if _, err := svc.RunNewsProcessingBatch(ctx, "jin10", 50, 50); err != nil {
		t.Fatalf("process news: %v", err)
	}
	if _, err := svc.PruneRawNewsRetention(ctx, now); err != nil {
		t.Fatalf("prune raw news retention: %v", err)
	}
	if _, err := svc.CreateRawNews(ctx, req); err != nil {
		t.Fatalf("recreate duplicate raw news: %v", err)
	}
	if _, err := svc.RunNewsProcessingBatch(ctx, "jin10", 50, 50); err != nil {
		t.Fatalf("process duplicate news: %v", err)
	}
	events, err := svc.ListNewsEvents(ctx, NewsEventListFilter{Source: "jin10", Limit: 10})
	if err != nil {
		t.Fatalf("list news events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events len = %d, want deduped event after raw prune", len(events))
	}
}

func TestNewsLinkCandidateUpsertReturnsStoredID(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()

	event, err := svc.CreateNewsEvent(ctx, NewsEvent{Source: "jin10", Title: "半导体设备订单"})
	if err != nil {
		t.Fatalf("create event: %v", err)
	}
	first, err := svc.store.UpsertNewsLinkCandidate(ctx, NewsLinkCandidate{
		ID:          "candidate-original",
		NewsEventID: event.ID,
		Symbol:      "688012",
		Market:      "SH",
		MatchMethod: "keyword",
		Score:       0.7,
		Reason:      "first",
	})
	if err != nil {
		t.Fatalf("upsert first: %v", err)
	}
	second, err := svc.store.UpsertNewsLinkCandidate(ctx, NewsLinkCandidate{
		ID:          "candidate-new",
		NewsEventID: event.ID,
		Symbol:      "688012",
		Market:      "SH",
		MatchMethod: "keyword",
		Score:       0.9,
		Reason:      "updated",
	})
	if err != nil {
		t.Fatalf("upsert second: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("second id = %q, want stored id %q", second.ID, first.ID)
	}
	reloaded, err := svc.store.GetNewsLinkCandidate(ctx, second.ID)
	if err != nil {
		t.Fatalf("get returned candidate: %v", err)
	}
	if reloaded.Score != 0.9 || reloaded.Reason != "updated" {
		t.Fatalf("reloaded candidate = %+v, want updated fields", reloaded)
	}
}

func TestNewsLinkCandidateBatchUpsertPreservesMonitorStatus(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()

	event, err := svc.CreateNewsEvent(ctx, NewsEvent{Source: "jin10", Title: "半导体设备订单"})
	if err != nil {
		t.Fatalf("create event: %v", err)
	}
	first, err := svc.store.UpsertNewsLinkCandidate(ctx, NewsLinkCandidate{
		ID:          "candidate-original",
		NewsEventID: event.ID,
		Symbol:      "688012",
		Market:      "SH",
		MatchMethod: "keyword",
		Score:       0.7,
		Reason:      "first",
	})
	if err != nil {
		t.Fatalf("upsert first: %v", err)
	}
	if err := svc.store.MarkNewsLinkCandidateMonitorStatus(ctx, first.ID, NewsLinkMonitorStatusHit, "hit-1", time.Now()); err != nil {
		t.Fatalf("mark monitor status: %v", err)
	}
	if err := svc.store.UpsertNewsLinkCandidates(ctx, []NewsLinkCandidate{{
		ID:            "candidate-new",
		NewsEventID:   event.ID,
		Symbol:        "688012",
		Market:        "SH",
		MatchMethod:   "keyword",
		Score:         0.9,
		Reason:        "updated",
		MonitorStatus: NewsLinkMonitorStatusPending,
	}}); err != nil {
		t.Fatalf("batch upsert: %v", err)
	}
	reloaded, err := svc.store.GetNewsLinkCandidate(ctx, first.ID)
	if err != nil {
		t.Fatalf("get candidate: %v", err)
	}
	if reloaded.ID != first.ID || reloaded.Score != 0.9 || reloaded.Reason != "updated" {
		t.Fatalf("reloaded candidate = %+v, want updated fields on original row", reloaded)
	}
	if reloaded.MonitorStatus != NewsLinkMonitorStatusHit || reloaded.MonitorHitID != "hit-1" {
		t.Fatalf("monitor fields = status %q hit %q, want preserved hit", reloaded.MonitorStatus, reloaded.MonitorHitID)
	}
}
