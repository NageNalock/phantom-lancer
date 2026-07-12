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

func TestAnnouncementSyncAdvancesAIRevisionAtomicallyOnMaterialChange(t *testing.T) {
	ctx := context.Background()
	svc, cleanup := newStockProfileTestService(t)
	defer cleanup()
	profile, err := svc.store.UpsertStockProfile(ctx, StockProfile{
		Symbol: "000001", Market: "SZ", InstrumentType: InstrumentTypeStock, Name: "平安银行",
		ProfileText: "基础画像", ProfileTextZh: "基础画像", BaseProfileHash: "base-v1",
		BaseProfileUpdatedAt: time.Now().Add(-time.Hour), AIProfileStatus: StockProfileAIStatusMissing,
	})
	if err != nil {
		t.Fatal(err)
	}
	before, exists, err := svc.store.GetStockProfileAIState(ctx, profile.Symbol)
	if err != nil || !exists {
		t.Fatalf("initial state exists=%v err=%v", exists, err)
	}
	now := time.Now()
	item := StockV2Announcement{
		Source: StockV2AnnouncementSourceCninfo, Symbol: profile.Symbol, Market: profile.Market,
		AnnouncementID: "ann-1", Title: "重大合同公告", ContentHash: "hash-v1", FetchedAt: now,
	}
	state := AnnouncementSyncState{
		Source: StockV2AnnouncementSourceCninfo, Market: profile.Market,
		CoveredThrough: now, LastSuccessAt: now,
	}
	if inserted, err := svc.store.CommitAnnouncementSyncBatch(ctx, []StockV2Announcement{item}, []AnnouncementSyncState{state}); err != nil || len(inserted) != 1 {
		t.Fatalf("first sync inserted=%d err=%v", len(inserted), err)
	}
	afterFirst, _, err := svc.store.GetStockProfileAIState(ctx, profile.Symbol)
	if err != nil {
		t.Fatal(err)
	}
	if afterFirst.AnnouncementRevision != before.AnnouncementRevision+1 || afterFirst.DesiredInputVersion == before.DesiredInputVersion {
		t.Fatalf("first revision before=%+v after=%+v", before, afterFirst)
	}

	item.FetchedAt = now.Add(time.Minute)
	if inserted, err := svc.store.CommitAnnouncementSyncBatch(ctx, []StockV2Announcement{item}, []AnnouncementSyncState{state}); err != nil || len(inserted) != 0 {
		t.Fatalf("duplicate sync inserted=%d err=%v", len(inserted), err)
	}
	afterDuplicate, _, _ := svc.store.GetStockProfileAIState(ctx, profile.Symbol)
	if afterDuplicate.AnnouncementRevision != afterFirst.AnnouncementRevision {
		t.Fatalf("duplicate advanced revision: first=%d duplicate=%d", afterFirst.AnnouncementRevision, afterDuplicate.AnnouncementRevision)
	}

	item.Title = "重大合同公告（更正）"
	if _, err := svc.store.CommitAnnouncementSyncBatch(ctx, []StockV2Announcement{item}, []AnnouncementSyncState{state}); err != nil {
		t.Fatal(err)
	}
	afterChange, _, _ := svc.store.GetStockProfileAIState(ctx, profile.Symbol)
	if afterChange.AnnouncementRevision != afterFirst.AnnouncementRevision+1 {
		t.Fatalf("material change revision=%d, want %d", afterChange.AnnouncementRevision, afterFirst.AnnouncementRevision+1)
	}

	rollbackItem := item
	rollbackItem.AnnouncementID = "ann-rollback"
	rollbackItem.ContentHash = "hash-rollback"
	if _, err := svc.store.CommitAnnouncementSyncBatch(ctx, []StockV2Announcement{rollbackItem}, []AnnouncementSyncState{{
		Source: StockV2AnnouncementSourceCninfo,
	}}); err == nil {
		t.Fatal("invalid cursor state unexpectedly committed")
	}
	afterRollback, _, _ := svc.store.GetStockProfileAIState(ctx, profile.Symbol)
	if afterRollback.AnnouncementRevision != afterChange.AnnouncementRevision {
		t.Fatalf("rolled-back sync advanced revision: before=%d after=%d", afterChange.AnnouncementRevision, afterRollback.AnnouncementRevision)
	}
	count, err := svc.store.CountAnnouncements(ctx, AnnouncementListFilter{Symbol: profile.Symbol})
	if err != nil || count != 1 {
		t.Fatalf("rolled-back announcement count=%d err=%v", count, err)
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
