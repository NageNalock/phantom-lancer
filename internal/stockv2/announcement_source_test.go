package stockv2

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
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

func TestFetchMarketAnnouncementsPage(t *testing.T) {
	var gotForm url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse form: %v", err)
		}
		gotForm = r.Form
		_ = json.NewEncoder(w).Encode(map[string]any{
			"totalAnnouncement": 3,
			"announcements": []map[string]any{
				{
					"secCode":              "000001",
					"orgId":                "gssz0000001",
					"announcementId":       "a-1",
					"announcementTitle":    "第一份公告",
					"announcementTypeName": "日常公告",
					"adjunctUrl":           "finalpage/a-1.pdf",
					"announcementTime":     "2026-07-10 08:00:00",
				},
				{
					"secCode":           "300001",
					"announcementId":    "a-2",
					"announcementTitle": "重大合同公告",
					"announcementTime":  "2026-07-10 09:00:00",
				},
			},
		})
	}))
	defer server.Close()

	source := NewAnnouncementSource(server.Client())
	source.queryURL = server.URL
	start := time.Date(2026, 7, 9, 18, 0, 0, 0, time.Local)
	end := time.Date(2026, 7, 10, 12, 0, 0, 0, time.Local)
	page, err := source.FetchMarketAnnouncementsPage(context.Background(), "sz", 1, 2, start, end)
	if err != nil {
		t.Fatalf("fetch page: %v", err)
	}
	if page.Market != "SZ" || page.Page != 1 || page.RawCount != 2 || page.Total != 3 || !page.HasMore {
		t.Fatalf("unexpected page: %+v", page)
	}
	if len(page.Announcements) != 2 || page.Announcements[0].Symbol != "000001" || page.Announcements[1].Symbol != "300001" {
		t.Fatalf("unexpected announcements: %+v", page.Announcements)
	}
	if !page.Announcements[1].Major {
		t.Fatalf("major announcement not classified: %+v", page.Announcements[1])
	}
	if gotForm.Get("stock") != "" || gotForm.Get("column") != "szse" || gotForm.Get("pageNum") != "1" {
		t.Fatalf("unexpected market form: %+v", gotForm)
	}
	if gotForm.Get("seDate") != "2026-07-09~2026-07-10" {
		t.Fatalf("seDate=%q", gotForm.Get("seDate"))
	}
}

func TestFetchAnnouncementsKeepsSingleSymbolPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse form: %v", err)
		}
		if got := r.Form.Get("stock"); got != "000001,gssz0000001" {
			t.Fatalf("stock=%q", got)
		}
		_, _ = w.Write([]byte(`{"announcements":[{"announcementId":"manual-1","announcementTitle":"手动路径公告","announcementTime":"2026-07-10"}]}`))
	}))
	defer server.Close()

	source := NewAnnouncementSource(server.Client())
	source.queryURL = server.URL
	source.orgCache["SZ:000001"] = "gssz0000001"
	items, _, err := source.FetchAnnouncements(context.Background(), StockV2Instrument{Symbol: "000001", Market: "SZ", InstrumentType: InstrumentTypeStock}, 20)
	if err != nil {
		t.Fatalf("fetch single symbol: %v", err)
	}
	if len(items) != 1 || items[0].Symbol != "000001" || items[0].AnnouncementID != "manual-1" {
		t.Fatalf("unexpected items: %+v", items)
	}
}
