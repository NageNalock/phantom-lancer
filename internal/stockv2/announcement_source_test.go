package stockv2

import (
	"strings"
	"testing"
)

func TestParseCninfoOrgID(t *testing.T) {
	body := []byte(`{"stockList":[{"code":"000001","orgId":"gssz0000001"},{"code":"000002","orgId":"gssz0000002"}]}`)
	if got := parseCninfoOrgID(body, "000002"); got != "gssz0000002" {
		t.Fatalf("orgID=%q", got)
	}
}

func TestParseCninfoAnnouncementsClassifiesMajor(t *testing.T) {
	body := []byte(`{
		"announcements":[{
			"announcementId":"121",
			"announcementTitle":"关于重大资产重组停牌的公告",
			"category":"重大事项",
			"adjunctUrl":"finalpage/2026-07-09/test.pdf",
			"announcementTime":1783526400000
		}]
	}`)
	items, err := parseCninfoAnnouncements(body, StockV2Instrument{Symbol: "000001", Market: "SZ"}, "gssz0000001")
	if err != nil {
		t.Fatalf("parse announcements: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("len=%d", len(items))
	}
	got := items[0]
	if !got.Major || got.MajorReason == "" {
		t.Fatalf("major=%v reason=%q", got.Major, got.MajorReason)
	}
	if got.ContentHash == "" || !strings.HasPrefix(got.PDFURL, "https://static.cninfo.com.cn/") {
		t.Fatalf("hash=%q pdf=%q", got.ContentHash, got.PDFURL)
	}
}
