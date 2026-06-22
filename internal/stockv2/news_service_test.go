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
	unprocessed, err := svc.ListUnprocessedRawNews(ctx, time.Now().Add(time.Hour), 10)
	if err != nil {
		t.Fatalf("list unprocessed raw news: %v", err)
	}
	if len(unprocessed) != 3 {
		t.Fatalf("unprocessed len = %d, want 3", len(unprocessed))
	}
}

func TestNewsEventCreateAndUnprocessedWindow(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()

	raw, err := svc.CreateRawNews(ctx, RequestCreateRawNews{
		Source:   "jin10",
		SourceID: "event-raw-1",
		Title:    "AI 服务器订单变化",
	})
	if err != nil {
		t.Fatalf("create raw news: %v", err)
	}
	eventTime := time.Now().Add(-2 * time.Minute)
	event, err := svc.CreateNewsEvent(ctx, RequestCreateNewsEvent{
		RawNewsID:  raw.ID,
		Title:      "AI 服务器订单变化",
		Summary:    "消息提到 AI 服务器订单变化。",
		Language:   "zh-CN",
		Source:     "jin10",
		EventTime:  eventTime,
		Importance: NewsImportanceHigh,
		Tags:       []string{"AI", "AI", "服务器"},
		Topics:     []string{"算力"},
	})
	if err != nil {
		t.Fatalf("create news event: %v", err)
	}
	if event.RawNewsID != raw.ID || event.Importance != NewsImportanceHigh {
		t.Fatalf("event = %+v", event)
	}
	if len(event.Tags) != 2 {
		t.Fatalf("tags = %+v, want compacted tags", event.Tags)
	}
	duplicate, err := svc.CreateNewsEvent(ctx, RequestCreateNewsEvent{
		RawNewsID: raw.ID,
		Title:     "同一 RawNews 再次标准化",
		Source:    "jin10",
	})
	if err != nil {
		t.Fatalf("create duplicate event: %v", err)
	}
	if duplicate.ID != event.ID {
		t.Fatalf("duplicate event id = %q, want %q", duplicate.ID, event.ID)
	}
	count, err := svc.CountNewsEvents(ctx, NewsEventListFilter{Source: "jin10"})
	if err != nil {
		t.Fatalf("count events: %v", err)
	}
	if count != 1 {
		t.Fatalf("event count = %d, want 1", count)
	}
	items, err := svc.ListUnprocessedNewsEvents(ctx, time.Now(), 10)
	if err != nil {
		t.Fatalf("list unprocessed events: %v", err)
	}
	if len(items) != 1 || items[0].ID != event.ID {
		t.Fatalf("unprocessed events = %+v", items)
	}
}

func TestNewsLinkCandidateUpsert(t *testing.T) {
	svc, cleanup := newStrategyTestService(t)
	defer cleanup()
	ctx := context.Background()

	event, err := svc.CreateNewsEvent(ctx, RequestCreateNewsEvent{
		Title:  "半导体设备政策更新",
		Source: "jin10",
	})
	if err != nil {
		t.Fatalf("create event: %v", err)
	}
	first, err := svc.UpsertNewsLinkCandidate(ctx, RequestUpsertNewsLinkCandidate{
		NewsEventID:  event.ID,
		Symbol:       "688012",
		Market:       "SH",
		MatchMethod:  "keyword",
		Score:        0.55,
		Reason:       "命中半导体设备关键词",
		MatchedTerms: []string{"半导体", "设备"},
	})
	if err != nil {
		t.Fatalf("upsert candidate: %v", err)
	}
	second, err := svc.UpsertNewsLinkCandidate(ctx, RequestUpsertNewsLinkCandidate{
		NewsEventID:  event.ID,
		Symbol:       "688012",
		Market:       "SH",
		MatchMethod:  "keyword",
		Score:        0.82,
		Reason:       "命中公司别名与行业关键词",
		MatchedTerms: []string{"公司别名"},
	})
	if err != nil {
		t.Fatalf("upsert candidate again: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("candidate id = %q, want %q", second.ID, first.ID)
	}
	if second.Score != 0.82 || len(second.MatchedTerms) != 1 {
		t.Fatalf("candidate after upsert = %+v", second)
	}
	count, err := svc.CountNewsLinkCandidates(ctx, NewsLinkCandidateListFilter{NewsEventID: event.ID})
	if err != nil {
		t.Fatalf("count candidates: %v", err)
	}
	if count != 1 {
		t.Fatalf("candidate count = %d, want 1", count)
	}
	list, err := svc.ListNewsLinkCandidates(ctx, NewsLinkCandidateListFilter{Symbol: "688012", Limit: 10})
	if err != nil {
		t.Fatalf("list candidates: %v", err)
	}
	if len(list) != 1 || list[0].Score != 0.82 {
		t.Fatalf("candidate list = %+v", list)
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
	if _, err := svc.CreateNewsEvent(ctx, RequestCreateNewsEvent{Source: "jin10"}); !errors.Is(err, ErrInvalidNewsEventTitle) {
		t.Fatalf("news event err = %v, want ErrInvalidNewsEventTitle", err)
	}
	if _, err := svc.CreateNewsEvent(ctx, RequestCreateNewsEvent{Title: "missing source"}); !errors.Is(err, ErrInvalidNewsEventSource) {
		t.Fatalf("news event source err = %v, want ErrInvalidNewsEventSource", err)
	}
	if _, err := svc.UpsertNewsLinkCandidate(ctx, RequestUpsertNewsLinkCandidate{
		NewsEventID: "missing",
		Symbol:      "000001",
		MatchMethod: "keyword",
		Score:       0.1,
	}); !errors.Is(err, ErrNewsEventNotFound) {
		t.Fatalf("candidate missing event err = %v, want ErrNewsEventNotFound", err)
	}
	if _, err := svc.UpsertNewsLinkCandidate(ctx, RequestUpsertNewsLinkCandidate{Score: -1}); !errors.Is(err, ErrInvalidNewsLinkCandidateKey) {
		t.Fatalf("candidate key err = %v, want ErrInvalidNewsLinkCandidateKey", err)
	}
	event, err := svc.CreateNewsEvent(ctx, RequestCreateNewsEvent{Title: "valid event", Source: "jin10"})
	if err != nil {
		t.Fatalf("create valid event: %v", err)
	}
	if _, err := svc.UpsertNewsLinkCandidate(ctx, RequestUpsertNewsLinkCandidate{
		NewsEventID: event.ID,
		Symbol:      "000001",
		MatchMethod: "keyword",
		Score:       -1,
	}); !errors.Is(err, ErrInvalidNewsLinkCandidate) {
		t.Fatalf("candidate score err = %v, want ErrInvalidNewsLinkCandidate", err)
	}
}
