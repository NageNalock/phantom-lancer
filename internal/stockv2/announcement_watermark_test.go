package stockv2

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestAnnouncementUpsertsPreserveFirstFetchedAt(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newStockProfileTestService(t)
	defer cleanup()

	firstFetchedAt := time.Now().Add(-3 * time.Hour).Truncate(time.Microsecond)
	announcement := StockV2Announcement{
		Source:      StockV2AnnouncementSourceCninfo,
		Symbol:      "000001",
		Market:      "SZ",
		Title:       "首次入库",
		ContentHash: "stable-hash",
		PublishedAt: firstFetchedAt.Add(-time.Hour),
		FetchedAt:   firstFetchedAt,
	}
	if inserted, err := svc.store.UpsertAnnouncements(ctx, []StockV2Announcement{announcement}); err != nil || inserted != 1 {
		t.Fatalf("initial upsert inserted=%d err=%v", inserted, err)
	}

	announcement.Title = "市场同步更新标题"
	announcement.FetchedAt = firstFetchedAt.Add(time.Hour)
	if _, err := svc.store.CommitAnnouncementSyncBatch(ctx, []StockV2Announcement{announcement}, []AnnouncementSyncState{{
		Source:         StockV2AnnouncementSourceCninfo,
		Market:         "SZ",
		CoveredThrough: firstFetchedAt.Add(2 * time.Hour),
		LastSuccessAt:  firstFetchedAt.Add(2 * time.Hour),
	}}); err != nil {
		t.Fatal(err)
	}

	announcement.Title = "单标的同步再次更新标题"
	announcement.FetchedAt = firstFetchedAt.Add(2 * time.Hour)
	if _, err := svc.store.UpsertAnnouncements(ctx, []StockV2Announcement{announcement}); err != nil {
		t.Fatal(err)
	}
	stored, err := svc.store.ListAnnouncements(ctx, AnnouncementListFilter{Symbol: announcement.Symbol, Limit: 1})
	if err != nil || len(stored) != 1 {
		t.Fatalf("stored announcements=%+v err=%v", stored, err)
	}
	if !stored[0].FetchedAt.Equal(firstFetchedAt) {
		t.Fatalf("fetched_at=%v, want first ingestion %v", stored[0].FetchedAt, firstFetchedAt)
	}
	if stored[0].Title != announcement.Title {
		t.Fatalf("title=%q, want refreshed metadata %q", stored[0].Title, announcement.Title)
	}
}

func TestAnnouncementsAfterAIProfileIncludesDelayedPublication(t *testing.T) {
	aiUpdatedAt := time.Now().Add(-time.Hour)
	delayed := StockV2Announcement{
		PublishedAt: aiUpdatedAt.Add(-24 * time.Hour),
		FetchedAt:   aiUpdatedAt.Add(time.Minute),
	}
	if got := announcementsAfterAIProfile([]StockV2Announcement{delayed}, aiUpdatedAt); len(got) != 1 {
		t.Fatalf("delayed announcement was filtered: %+v", got)
	}
}

func TestRecentAnnouncementsBySymbolsKeepsBacklogBeyondTwenty(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newStockProfileTestService(t)
	defer cleanup()

	aiUpdatedAt := time.Now().Add(-time.Hour).Truncate(time.Microsecond)
	items := make([]StockV2Announcement, 0, 25)
	for i := 0; i < 25; i++ {
		fetchedAt := aiUpdatedAt.Add(-time.Hour)
		publishedAt := aiUpdatedAt.Add(-time.Duration(i+1) * time.Hour)
		if i >= 20 {
			// These five announcements were published earlier but only became visible
			// after the last AI summary. Publication-only ordering used to hide them
			// behind the twenty newer publication dates.
			fetchedAt = aiUpdatedAt.Add(time.Duration(i-19) * time.Minute)
		}
		items = append(items, StockV2Announcement{
			Source:      StockV2AnnouncementSourceCninfo,
			Symbol:      "000001",
			Market:      "SZ",
			Title:       fmt.Sprintf("公告 %02d", i),
			ContentHash: fmt.Sprintf("hash-%02d", i),
			PublishedAt: publishedAt,
			FetchedAt:   fetchedAt,
		})
	}
	if inserted, err := svc.store.UpsertAnnouncements(ctx, items); err != nil || inserted != len(items) {
		t.Fatalf("upsert backlog inserted=%d err=%v", inserted, err)
	}

	recent, err := svc.store.ListRecentAnnouncementsBySymbols(ctx, []string{"000001"}, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(recent["000001"]) != 25 {
		t.Fatalf("recent count=%d, want 25", len(recent["000001"]))
	}
	if got := announcementsAfterAIProfile(recent["000001"], aiUpdatedAt); len(got) != 5 {
		t.Fatalf("announcements after AI=%d, want delayed backlog 5", len(got))
	}
	stats, err := svc.store.LatestAnnouncementStats(ctx, []string{"000001"})
	if err != nil {
		t.Fatal(err)
	}
	wantFetchedAt := aiUpdatedAt.Add(5 * time.Minute)
	if !stats["000001"].LatestAnnouncementFetchedAt.Equal(wantFetchedAt) {
		t.Fatalf("latest fetched=%v, want %v", stats["000001"].LatestAnnouncementFetchedAt, wantFetchedAt)
	}
	if stats["000001"].LatestAnnouncementTitle != "公告 24" {
		t.Fatalf("latest title=%q, want delayed-ingestion title", stats["000001"].LatestAnnouncementTitle)
	}
}
