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
		"totalAnnouncement":1,
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

func TestClassifyMajorAnnouncementCoverage(t *testing.T) {
	tests := []struct {
		name     string
		title    string
		category string
	}{
		{name: "winning bid", title: "关于收到项目中标通知书的公告"},
		{name: "regulatory penalty", title: "关于收到行政处罚决定书的公告"},
		{name: "external guarantee", title: "关于新增对外担保额度的公告"},
		{name: "major investment", title: "关于重大投资项目进展的公告"},
		{name: "exchange inquiry", title: "关于收到上海证券交易所问询函的公告"},
		{name: "regulatory letter", title: "关于收到监管函的公告"},
		{name: "inquiry response", title: "关于审核问询函回复的公告"},
		{name: "regulatory response", title: "关于监管问询事项的回复公告"},
		{name: "executive change", title: "关于高级管理人员变动的公告"},
		{name: "manager resignation", title: "关于总经理辞职暨聘任总经理的公告"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			major, reason := classifyMajorAnnouncement(tt.title, tt.category)
			if !major || reason == "" {
				t.Fatalf("classifyMajorAnnouncement(%q, %q) = %v, %q", tt.title, tt.category, major, reason)
			}
		})
	}
}

func TestCninfoAnnouncementUpgradesOfficialPDFURLToHTTPS(t *testing.T) {
	item, ok := cninfoAnnouncementFromRaw(map[string]any{
		"secCode":           "000001",
		"announcementId":    "pdf-1",
		"announcementTitle": "测试公告",
		"announcementTime":  "2026-07-12 10:00:00",
		"adjunctUrl":        "http://static.cninfo.com.cn/finalpage/2026-07-12/test.pdf",
	}, "000001", "SZ", "org-1")
	if !ok || item.PDFURL != "https://static.cninfo.com.cn/finalpage/2026-07-12/test.pdf" {
		t.Fatalf("announcement = %+v, ok=%v", item, ok)
	}
}

func TestParseCninfoAnnouncementsFailsClosedOnUnknownEnvelope(t *testing.T) {
	for name, body := range map[string][]byte{
		"empty":                nil,
		"empty object":         []byte(`{}`),
		"unknown object":       []byte(`{"data":[]}`),
		"announcements object": []byte(`{"announcements":{}}`),
		"announcements null":   []byte(`{"totalAnnouncement":0,"announcements":null}`),
		"missing total":        []byte(`{"announcements":[]}`),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseCninfoMarketAnnouncementsPage(body, "SZ", 1, 30); err == nil {
				t.Fatal("unknown CNINFO response was accepted")
			}
		})
	}
	page, err := parseCninfoMarketAnnouncementsPage(
		[]byte(`{"totalAnnouncement":0,"announcements":[]}`), "SZ", 1, 30,
	)
	if err != nil || page.RawCount != 0 || page.HasMore {
		t.Fatalf("valid empty page = %+v, err=%v", page, err)
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
		_, _ = w.Write([]byte(`{"totalAnnouncement":1,"announcements":[{"announcementId":"manual-1","announcementTitle":"手动路径公告","announcementTime":"2026-07-10"}]}`))
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

func TestAnnouncementSourceBackoffRemainsFailedAndRetryable(t *testing.T) {
	svc := &Service{
		announcementSource: NewAnnouncementSource(nil),
		assetBackoff: map[string]time.Time{
			"announcement:cninfo:SZ": time.Now().Add(time.Hour),
		},
	}
	_, status, err := svc.fetchAndStoreAnnouncements(
		context.Background(),
		StockV2Instrument{Symbol: "000001", Market: "SZ", InstrumentType: InstrumentTypeStock},
	)
	if err == nil || status.Status != AssetAnnouncementStatusFailed {
		t.Fatalf("backoff status = %+v, err=%v", status, err)
	}
}
