package stockv2

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestAnnouncementBodyProcessorDoesNothingWithoutPDFToText(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	var requests atomic.Int64
	svc, cleanup := newStockProfileTestServiceWithClient(t, &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		requests.Add(1)
		return announcementBodyPDFResponse(), nil
	})})
	defer cleanup()
	seedMajorAnnouncementBodyCandidate(t, svc, "ann-no-parser", "https://static.cninfo.com.cn/finalpage/no-parser.pdf")

	result, err := svc.ProcessPendingMajorAnnouncementBodies(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.ParserAvailable || result.Claimed != 0 || requests.Load() != 0 {
		t.Fatalf("unexpected parser-less result=%+v requests=%d", result, requests.Load())
	}
	stored := getSingleAnnouncementForTest(t, svc, "000001")
	if stored.BodyStatus != AnnouncementBodyStatusMetadataOnly || stored.BodyAttemptCount != 0 {
		t.Fatalf("parser-less announcement mutated: %+v", stored)
	}
}

func TestAnnouncementBodyProcessorExtractsOfficialPDFAndAdvancesAIOutbox(t *testing.T) {
	auditPath := installFakePDFToText(t, strings.Repeat("这是巨潮资讯正式公告正文，包含重大合同条款、履约期限和风险提示。", 8))
	var requests atomic.Int64
	svc, cleanup := newStockProfileTestServiceWithClient(t, &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests.Add(1)
		if req.URL.Scheme != "https" || req.URL.Hostname() != "static.cninfo.com.cn" {
			t.Fatalf("unexpected PDF request URL: %s", req.URL)
		}
		return announcementBodyPDFResponse(), nil
	})})
	defer cleanup()
	ctx := context.Background()
	if _, err := svc.store.UpsertStockProfile(ctx, StockProfile{
		Symbol: "000001", Market: "SZ", InstrumentType: InstrumentTypeStock, Name: "平安银行",
		ProfileText: "基础画像", ProfileTextZh: "基础画像", BaseProfileHash: "base-v1",
		BaseProfileUpdatedAt: time.Now(), AIProfileStatus: StockProfileAIStatusReady,
	}); err != nil {
		t.Fatal(err)
	}
	seedMajorAnnouncementBodyCandidate(t, svc, "ann-ready", "https://static.cninfo.com.cn/finalpage/ready.pdf")
	before, exists, err := svc.store.GetStockProfileAIState(ctx, "000001")
	if err != nil || !exists {
		t.Fatalf("state before exists=%v err=%v", exists, err)
	}

	result, err := svc.ProcessPendingMajorAnnouncementBodies(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !result.ParserAvailable || result.Ready != 1 || result.Claimed != 1 || requests.Load() != 1 {
		t.Fatalf("unexpected extraction result=%+v requests=%d", result, requests.Load())
	}
	stored := getSingleAnnouncementForTest(t, svc, "000001")
	if stored.BodyStatus != AnnouncementBodyStatusTextReady || stored.BodyHash == "" ||
		stored.BodyAttemptCount != 1 || stored.BodyContentBytes == 0 ||
		!strings.Contains(stored.BodyTextExcerpt, "重大合同条款") {
		t.Fatalf("stored extracted body=%+v", stored)
	}
	after, exists, err := svc.store.GetStockProfileAIState(ctx, "000001")
	if err != nil || !exists {
		t.Fatalf("state after exists=%v err=%v", exists, err)
	}
	if after.AnnouncementRevision != before.AnnouncementRevision+1 ||
		after.DesiredInputVersion == before.DesiredInputVersion ||
		after.DesiredTriggerReason != AssetAIDecisionAnnouncement {
		t.Fatalf("body completion did not advance AI target: before=%+v after=%+v", before, after)
	}
	if err := svc.reconcileStockProfileAIQueueBatch(ctx, 100); err != nil {
		t.Fatal(err)
	}
	queue, err := svc.store.GetStockProfileAIQueueItem(ctx, "000001")
	if err != nil {
		t.Fatal(err)
	}
	if queue.DesiredInputVersion != after.DesiredInputVersion || queue.TriggerReason != AssetAIDecisionAnnouncement {
		t.Fatalf("AI outbox not reconciled: queue=%+v state=%+v", queue, after)
	}
	stats, err := svc.store.LatestAnnouncementStats(ctx, []string{"000001"})
	if err != nil || stats["000001"].MajorAnnouncementContentUnavailableCount != 0 {
		t.Fatalf("ready body still unavailable: stats=%+v err=%v", stats, err)
	}

	audit, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(audit)), "\n")
	if len(lines) != 3 || !strings.Contains(lines[0], "-f 1 -l 8") || lines[1] != "600" {
		t.Fatalf("pdftotext audit=%q", audit)
	}
	if _, err := os.Stat(lines[2]); !os.IsNotExist(err) {
		t.Fatalf("temporary PDF was not removed: path=%q err=%v", lines[2], err)
	}
}

func TestAnnouncementBodyProcessorRejectsNonOfficialURLWithoutRequest(t *testing.T) {
	installFakePDFToText(t, strings.Repeat("有效正文", 100))
	var requests atomic.Int64
	svc, cleanup := newStockProfileTestServiceWithClient(t, &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		requests.Add(1)
		return announcementBodyPDFResponse(), nil
	})})
	defer cleanup()
	seedMajorAnnouncementBodyCandidate(t, svc, "ann-untrusted", "https://example.invalid/announcement.pdf")

	result, err := svc.ProcessPendingMajorAnnouncementBodies(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Failed != 1 || requests.Load() != 0 {
		t.Fatalf("untrusted URL result=%+v requests=%d", result, requests.Load())
	}
	stored := getSingleAnnouncementForTest(t, svc, "000001")
	if stored.BodyStatus != AnnouncementBodyStatusFailed || stored.BodyTextExcerpt != "" {
		t.Fatalf("untrusted URL became ready: %+v", stored)
	}
}

func TestAnnouncementBodyProcessorDoesNotTreatEmptyPDFTextAsReady(t *testing.T) {
	installFakePDFToText(t, "重大合同公告")
	svc, cleanup := newStockProfileTestServiceWithClient(t, &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return announcementBodyPDFResponse(), nil
	})})
	defer cleanup()
	seedMajorAnnouncementBodyCandidate(t, svc, "ann-scan", "https://static.cninfo.com.cn/finalpage/scan.pdf")

	result, err := svc.ProcessPendingMajorAnnouncementBodies(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Failed != 1 || result.Ready != 0 {
		t.Fatalf("empty extraction result=%+v", result)
	}
	stored := getSingleAnnouncementForTest(t, svc, "000001")
	if stored.BodyStatus != AnnouncementBodyStatusFailed || stored.BodyTextExcerpt != "" || stored.BodyHash != "" {
		t.Fatalf("empty extraction became ready: %+v", stored)
	}
	stats, err := svc.store.LatestAnnouncementStats(context.Background(), []string{"000001"})
	if err != nil || stats["000001"].MajorAnnouncementContentUnavailableCount != 1 {
		t.Fatalf("empty extraction did not block readiness: stats=%+v err=%v", stats, err)
	}
}

func TestAnnouncementBodyDailyRequestBudgetIsPersistent(t *testing.T) {
	installFakePDFToText(t, strings.Repeat("这是可提取的公告正文。", 20))
	var requests atomic.Int64
	svc, cleanup := newStockProfileTestServiceWithClient(t, &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		requests.Add(1)
		return announcementBodyPDFResponse(), nil
	})})
	defer cleanup()
	ctx := context.Background()
	items := make([]StockV2Announcement, 0, announcementBodyDailyRequestLimit+1)
	for index := 0; index <= announcementBodyDailyRequestLimit; index++ {
		id := "budget-" + strconv.Itoa(index)
		items = append(items, StockV2Announcement{
			Source: StockV2AnnouncementSourceCninfo, Symbol: "000001", Market: "SZ",
			AnnouncementID: id, Title: "重大合同公告 " + id, ContentHash: "hash-" + id,
			PDFURL: "https://static.cninfo.com.cn/finalpage/" + id + ".pdf",
			Major:  true, MajorReason: "重大合同", FetchedAt: time.Now(),
		})
	}
	if inserted, err := svc.store.UpsertAnnouncements(ctx, items); err != nil || inserted != len(items) {
		t.Fatalf("seed budget candidates inserted=%d err=%v", inserted, err)
	}
	var exhausted bool
	for run := 0; run < 10; run++ {
		result, err := svc.ProcessPendingMajorAnnouncementBodies(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if result.BudgetExhausted {
			exhausted = true
			break
		}
	}
	if !exhausted || requests.Load() != announcementBodyDailyRequestLimit {
		t.Fatalf("budget exhausted=%v requests=%d", exhausted, requests.Load())
	}
	stored, err := svc.store.ListAnnouncements(ctx, AnnouncementListFilter{Symbol: "000001", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	pending := 0
	for _, item := range stored {
		if item.BodyStatus == AnnouncementBodyStatusMetadataOnly {
			pending++
		}
	}
	if pending != 1 {
		t.Fatalf("pending after daily budget=%d, want 1", pending)
	}
}

func seedMajorAnnouncementBodyCandidate(t *testing.T, svc *Service, id, pdfURL string) {
	t.Helper()
	item := StockV2Announcement{
		Source: StockV2AnnouncementSourceCninfo, Symbol: "000001", Market: "SZ",
		AnnouncementID: id, Title: "重大合同公告", ContentHash: "hash-" + id,
		PDFURL: pdfURL, Major: true, MajorReason: "重大合同", FetchedAt: time.Now(),
	}
	if inserted, err := svc.store.UpsertAnnouncements(context.Background(), []StockV2Announcement{item}); err != nil || inserted != 1 {
		t.Fatalf("seed major announcement inserted=%d err=%v", inserted, err)
	}
}

func getSingleAnnouncementForTest(t *testing.T, svc *Service, symbol string) StockV2Announcement {
	t.Helper()
	items, err := svc.store.ListAnnouncements(context.Background(), AnnouncementListFilter{Symbol: symbol, Limit: 1})
	if err != nil || len(items) != 1 {
		t.Fatalf("get announcement items=%+v err=%v", items, err)
	}
	return items[0]
}

func announcementBodyPDFResponse() *http.Response {
	body := "%PDF-1.4\n1 0 obj\n<< /Type /Catalog >>\nendobj\n%%EOF\n"
	return &http.Response{
		StatusCode:    http.StatusOK,
		Header:        http.Header{"Content-Type": []string{"application/pdf"}},
		Body:          io.NopCloser(strings.NewReader(body)),
		ContentLength: int64(len(body)),
	}
}

func installFakePDFToText(t *testing.T, output string) string {
	t.Helper()
	dir := t.TempDir()
	auditPath := filepath.Join(dir, "audit.txt")
	script := `#!/bin/sh
printf '%s\n' "$*" > "$PDFTOTEXT_AUDIT_FILE"
/usr/bin/stat -c '%a' "$9" >> "$PDFTOTEXT_AUDIT_FILE"
printf '%s\n' "$9" >> "$PDFTOTEXT_AUDIT_FILE"
printf '%s' "$PDFTOTEXT_TEST_OUTPUT"
`
	path := filepath.Join(dir, "pdftotext")
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("PDFTOTEXT_AUDIT_FILE", auditPath)
	t.Setenv("PDFTOTEXT_TEST_OUTPUT", output)
	return auditPath
}
