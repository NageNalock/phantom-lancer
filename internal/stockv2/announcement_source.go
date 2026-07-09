package stockv2

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type AnnouncementSource struct {
	httpClient *http.Client
	mu         sync.Mutex
	orgCache   map[string]string
	loadedURLs map[string]bool
}

func NewAnnouncementSource(httpClient *http.Client) *AnnouncementSource {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &AnnouncementSource{httpClient: httpClient, orgCache: map[string]string{}, loadedURLs: map[string]bool{}}
}

func (s *AnnouncementSource) FetchAnnouncements(ctx context.Context, inst StockV2Instrument, limit int) ([]StockV2Announcement, AssetMaintenanceSourceStatus, error) {
	status := AssetMaintenanceSourceStatus{Source: StockV2AnnouncementSourceCninfo, Status: "ok", CheckedAt: time.Now()}
	if s == nil {
		status.Status = "skipped"
		status.Message = "source not configured"
		return nil, status, nil
	}
	if limit <= 0 {
		limit = 20
	}
	orgID, err := s.lookupOrgID(ctx, inst)
	if err != nil {
		status.Status = "failed"
		status.Message = err.Error()
		return nil, status, err
	}
	if orgID == "" {
		status.Status = "skipped"
		status.Message = "orgId not found"
		return nil, status, nil
	}
	form := url.Values{}
	form.Set("stock", fmt.Sprintf("%s,%s", stockCodeOnly(inst.Symbol), orgID))
	form.Set("tabName", "fulltext")
	form.Set("pageSize", fmt.Sprintf("%d", limit))
	form.Set("pageNum", "1")
	form.Set("column", cninfoColumn(inst.Market))
	form.Set("plate", cninfoPlate(inst.Market))
	form.Set("isHLtitle", "true")
	form.Set("sortName", "time")
	form.Set("sortType", "desc")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://www.cninfo.com.cn/new/hisAnnouncement/query", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, status, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Referer", "https://www.cninfo.com.cn/new/commonUrl/pageOfSearch?url=disclosure/list/search")
	resp, err := s.httpClient.Do(req)
	if err != nil {
		status.Status = "failed"
		status.Message = err.Error()
		return nil, status, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		status.Status = "failed"
		status.Message = fmt.Sprintf("cninfo status %d", resp.StatusCode)
		return nil, status, fmt.Errorf("cninfo status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		status.Status = "failed"
		status.Message = err.Error()
		return nil, status, err
	}
	items, err := parseCninfoAnnouncements(body, inst, orgID)
	if err != nil {
		status.Status = "failed"
		status.Message = err.Error()
		return nil, status, err
	}
	return items, status, nil
}

func (s *AnnouncementSource) lookupOrgID(ctx context.Context, inst StockV2Instrument) (string, error) {
	code := stockCodeOnly(inst.Symbol)
	key := strings.ToUpper(inst.Market) + ":" + code
	s.mu.Lock()
	if v := s.orgCache[key]; v != "" {
		s.mu.Unlock()
		return v, nil
	}
	s.mu.Unlock()
	urls := cninfoOrgURLs(inst.Market)
	for _, rawURL := range urls {
		s.mu.Lock()
		loaded := s.loadedURLs[rawURL]
		s.mu.Unlock()
		if loaded {
			continue
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return "", err
		}
		req.Header.Set("User-Agent", "Mozilla/5.0")
		resp, err := s.httpClient.Do(req)
		if err != nil {
			return "", err
		}
		body, readErr := func() ([]byte, error) {
			defer resp.Body.Close()
			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				return nil, fmt.Errorf("cninfo org status %d", resp.StatusCode)
			}
			return io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
		}()
		if readErr != nil {
			return "", readErr
		}
		orgs := parseCninfoOrgIDs(body)
		s.mu.Lock()
		for stockCode, orgID := range orgs {
			s.orgCache[strings.ToUpper(inst.Market)+":"+stockCode] = orgID
		}
		s.loadedURLs[rawURL] = true
		orgID := s.orgCache[key]
		s.mu.Unlock()
		if orgID != "" {
			return orgID, nil
		}
	}
	return "", nil
}

func cninfoOrgURLs(market string) []string {
	switch strings.ToUpper(strings.TrimSpace(market)) {
	case "SH":
		return []string{"http://www.cninfo.com.cn/new/data/sse_stock.json"}
	case "BJ":
		return []string{"http://www.cninfo.com.cn/new/data/bj_stock.json", "http://www.cninfo.com.cn/new/data/szse_stock.json"}
	default:
		return []string{"http://www.cninfo.com.cn/new/data/szse_stock.json"}
	}
}

func stockCodeOnly(symbol string) string {
	var digits strings.Builder
	for _, r := range symbol {
		if r >= '0' && r <= '9' {
			digits.WriteRune(r)
		}
	}
	code := digits.String()
	if len(code) > 6 {
		return code[len(code)-6:]
	}
	return code
}

func cninfoColumn(market string) string {
	switch strings.ToUpper(strings.TrimSpace(market)) {
	case "SH":
		return "sse"
	case "BJ":
		return "bj"
	default:
		return "szse"
	}
}

func cninfoPlate(market string) string {
	switch strings.ToUpper(strings.TrimSpace(market)) {
	case "SH":
		return "sh"
	case "BJ":
		return "bj"
	default:
		return "sz"
	}
}

func parseCninfoOrgID(body []byte, code string) string {
	return parseCninfoOrgIDs(body)[code]
}

func parseCninfoOrgIDs(body []byte) map[string]string {
	var payload any
	if err := json.Unmarshal(body, &payload); err != nil {
		return map[string]string{}
	}
	out := map[string]string{}
	collectCninfoOrgIDs(payload, out)
	return out
}

func collectCninfoOrgIDs(v any, out map[string]string) {
	switch x := v.(type) {
	case map[string]any:
		gotCode := firstJSONText(x, "code", "stockCode", "SECURITY_CODE")
		orgID := firstJSONText(x, "orgId", "orgID", "orgid", "org_id")
		if gotCode != "" && orgID != "" {
			out[gotCode] = orgID
		}
		for _, child := range x {
			collectCninfoOrgIDs(child, out)
		}
	case []any:
		for _, child := range x {
			collectCninfoOrgIDs(child, out)
		}
	}
}

func parseCninfoAnnouncements(body []byte, inst StockV2Instrument, orgID string) ([]StockV2Announcement, error) {
	var payload struct {
		Announcements []map[string]any `json:"announcements"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	out := make([]StockV2Announcement, 0, len(payload.Announcements))
	now := time.Now()
	for _, raw := range payload.Announcements {
		title := cleanAnnouncementTitle(firstJSONText(raw, "announcementTitle", "title"))
		if title == "" {
			continue
		}
		category := firstJSONText(raw, "category", "announcementTypeName", "categoryName")
		announcementID := firstJSONText(raw, "announcementId", "id")
		adjunctURL := firstJSONText(raw, "adjunctUrl", "adjunctURL")
		pdfURL := adjunctURL
		if pdfURL != "" && !strings.HasPrefix(pdfURL, "http") {
			pdfURL = "https://static.cninfo.com.cn/" + strings.TrimLeft(pdfURL, "/")
		}
		publishedAt := parseCninfoTime(raw["announcementTime"])
		if publishedAt.IsZero() {
			publishedAt = parseCninfoDate(firstJSONText(raw, "announcementTime", "publishTime", "date"))
		}
		major, reason := classifyMajorAnnouncement(title, category)
		hash := announcementContentHash(inst.Symbol, title, category, pdfURL, publishedAt)
		out = append(out, StockV2Announcement{
			ID:             generateID(),
			Source:         StockV2AnnouncementSourceCninfo,
			Symbol:         inst.Symbol,
			Market:         inst.Market,
			OrgID:          orgID,
			Title:          title,
			Category:       category,
			AnnouncementID: announcementID,
			PDFURL:         pdfURL,
			ContentHash:    hash,
			Major:          major,
			MajorReason:    reason,
			PublishedAt:    publishedAt,
			FetchedAt:      now,
			CreatedAt:      now,
			UpdatedAt:      now,
		})
	}
	return out, nil
}

func firstJSONText(m map[string]any, keys ...string) string {
	for _, key := range keys {
		if v, ok := m[key]; ok {
			switch x := v.(type) {
			case string:
				return strings.TrimSpace(x)
			case float64:
				if x == float64(int64(x)) {
					return fmt.Sprintf("%d", int64(x))
				}
				return fmt.Sprintf("%v", x)
			case json.Number:
				return x.String()
			}
		}
	}
	return ""
}

func parseCninfoTime(v any) time.Time {
	switch x := v.(type) {
	case float64:
		n := int64(x)
		if n > 10_000_000_000 {
			return time.UnixMilli(n)
		}
		if n > 0 {
			return time.Unix(n, 0)
		}
	case json.Number:
		if n, err := x.Int64(); err == nil {
			if n > 10_000_000_000 {
				return time.UnixMilli(n)
			}
			if n > 0 {
				return time.Unix(n, 0)
			}
		}
	}
	return time.Time{}
}

func parseCninfoDate(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02", "2006/01/02"} {
		if t, err := time.ParseInLocation(layout, raw, time.Local); err == nil {
			return t
		}
	}
	return time.Time{}
}

func cleanAnnouncementTitle(title string) string {
	title = strings.ReplaceAll(title, "<em>", "")
	title = strings.ReplaceAll(title, "</em>", "")
	return strings.TrimSpace(title)
}

func classifyMajorAnnouncement(title, category string) (bool, string) {
	text := strings.ToLower(title + " " + category)
	rules := []string{
		"重大资产重组", "重大事项", "控制权", "实际控制人", "收购", "并购", "要约",
		"停牌", "复牌", "业绩预告", "业绩快报", "利润分配", "分红", "回购",
		"定增", "非公开发行", "发行股份", "股权激励", "重大合同", "诉讼",
		"仲裁", "违规", "退市", "风险警示", "减持", "增持", "质押",
	}
	for _, rule := range rules {
		if strings.Contains(text, strings.ToLower(rule)) {
			return true, rule
		}
	}
	return false, ""
}

func announcementContentHash(symbol, title, category, pdfURL string, publishedAt time.Time) string {
	h := sha256.Sum256([]byte(strings.Join([]string{
		symbol,
		title,
		category,
		pdfURL,
		publishedAt.Format("2006-01-02"),
	}, "\x00")))
	return hex.EncodeToString(h[:])
}
