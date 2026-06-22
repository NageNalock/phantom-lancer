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
