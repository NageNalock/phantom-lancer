package stockv2

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

type AnnouncementSource struct {
	httpClient *http.Client
	queryURL   string
	mu         sync.Mutex
	orgCache   map[string]string
	loadedURLs map[string]bool
}

const cninfoAnnouncementQueryURL = "https://www.cninfo.com.cn/new/hisAnnouncement/query"

func NewAnnouncementSource(httpClient *http.Client) *AnnouncementSource {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &AnnouncementSource{httpClient: httpClient, queryURL: cninfoAnnouncementQueryURL, orgCache: map[string]string{}, loadedURLs: map[string]bool{}}
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
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.announcementQueryURL(), strings.NewReader(form.Encode()))
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

// FetchMarketAnnouncementsPage fetches one exchange-wide page without issuing
// one request per symbol. Callers own pagination and durable checkpointing.
func (s *AnnouncementSource) FetchMarketAnnouncementsPage(
	ctx context.Context,
	market string,
	page int,
	pageSize int,
	startAt time.Time,
	endAt time.Time,
) (AnnouncementMarketPage, error) {
	if s == nil || s.httpClient == nil {
		return AnnouncementMarketPage{}, fmt.Errorf("announcement source not configured")
	}
	market, err := normalizeCninfoMarket(market)
	if err != nil {
		return AnnouncementMarketPage{}, err
	}
	if page <= 0 {
		return AnnouncementMarketPage{}, fmt.Errorf("invalid announcement page %d", page)
	}
	if pageSize <= 0 {
		pageSize = announcementSyncDefaultPageSize
	}
	if pageSize > announcementSyncMaxPageSize {
		pageSize = announcementSyncMaxPageSize
	}
	if !startAt.IsZero() && !endAt.IsZero() && startAt.After(endAt) {
		return AnnouncementMarketPage{}, fmt.Errorf("announcement window start is after end")
	}

	form := url.Values{}
	form.Set("tabName", "fulltext")
	form.Set("pageSize", strconv.Itoa(pageSize))
	form.Set("pageNum", strconv.Itoa(page))
	form.Set("column", cninfoColumn(market))
	form.Set("plate", cninfoPlate(market))
	form.Set("isHLtitle", "true")
	form.Set("sortName", "time")
	form.Set("sortType", "desc")
	if !startAt.IsZero() || !endAt.IsZero() {
		startDate := startAt
		if startDate.IsZero() {
			startDate = endAt
		}
		endDate := endAt
		if endDate.IsZero() {
			endDate = startAt
		}
		form.Set("seDate", startDate.Format("2006-01-02")+"~"+endDate.Format("2006-01-02"))
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.announcementQueryURL(), strings.NewReader(form.Encode()))
	if err != nil {
		return AnnouncementMarketPage{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Referer", "https://www.cninfo.com.cn/new/commonUrl/pageOfSearch?url=disclosure/list/search")
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return AnnouncementMarketPage{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return AnnouncementMarketPage{}, fmt.Errorf("cninfo status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		return AnnouncementMarketPage{}, err
	}
	return parseCninfoMarketAnnouncementsPage(body, market, page, pageSize)
}

func (s *AnnouncementSource) announcementQueryURL() string {
	if s != nil && strings.TrimSpace(s.queryURL) != "" {
		return s.queryURL
	}
	return cninfoAnnouncementQueryURL
}

func normalizeCninfoMarket(market string) (string, error) {
	market = strings.ToUpper(strings.TrimSpace(market))
	switch market {
	case "SH", "SZ", "BJ":
		return market, nil
	default:
		return "", fmt.Errorf("unsupported announcement market %q", market)
	}
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

type cninfoAnnouncementsPayload struct {
	Announcements     []map[string]any `json:"announcements"`
	TotalAnnouncement any              `json:"totalAnnouncement"`
	TotalRecordNum    any              `json:"totalRecordNum"`
}

func parseCninfoAnnouncements(body []byte, inst StockV2Instrument, orgID string) ([]StockV2Announcement, error) {
	payload, err := decodeCninfoAnnouncements(body)
	if err != nil {
		return nil, err
	}
	out := make([]StockV2Announcement, 0, len(payload.Announcements))
	for _, raw := range payload.Announcements {
		if item, ok := cninfoAnnouncementFromRaw(raw, inst.Symbol, inst.Market, orgID); ok {
			out = append(out, item)
		}
	}
	return out, nil
}

func parseCninfoMarketAnnouncementsPage(body []byte, market string, page, pageSize int) (AnnouncementMarketPage, error) {
	payload, err := decodeCninfoAnnouncements(body)
	if err != nil {
		return AnnouncementMarketPage{}, err
	}
	items := make([]StockV2Announcement, 0, len(payload.Announcements))
	for index, raw := range payload.Announcements {
		symbol := stockCodeOnly(firstJSONText(raw, "secCode", "stockCode", "code", "SECURITY_CODE"))
		orgID := firstJSONText(raw, "orgId", "orgID", "orgid", "org_id")
		item, ok := cninfoAnnouncementFromRaw(raw, symbol, market, orgID)
		if !ok {
			return AnnouncementMarketPage{}, fmt.Errorf("invalid cninfo announcement at page %d item %d", page, index+1)
		}
		items = append(items, item)
	}
	total := cninfoInt(payload.TotalAnnouncement)
	if candidate := cninfoInt(payload.TotalRecordNum); candidate > total {
		total = candidate
	}
	rawCount := len(payload.Announcements)
	if total < rawCount {
		total = rawCount
	}
	hasMore := total > page*pageSize
	if cninfoInt(payload.TotalAnnouncement) == 0 && cninfoInt(payload.TotalRecordNum) == 0 {
		hasMore = rawCount >= pageSize
	}
	return AnnouncementMarketPage{
		Market:        market,
		Page:          page,
		PageSize:      pageSize,
		Total:         total,
		RawCount:      rawCount,
		HasMore:       hasMore,
		Announcements: items,
	}, nil
}

func decodeCninfoAnnouncements(body []byte) (cninfoAnnouncementsPayload, error) {
	if len(strings.TrimSpace(string(body))) == 0 {
		return cninfoAnnouncementsPayload{}, errors.New("empty cninfo announcement response")
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(body, &envelope); err != nil {
		return cninfoAnnouncementsPayload{}, err
	}
	announcements, ok := envelope["announcements"]
	if !ok {
		return cninfoAnnouncementsPayload{}, errors.New("cninfo announcement response is missing announcements")
	}
	trimmedAnnouncements := strings.TrimSpace(string(announcements))
	if !strings.HasPrefix(trimmedAnnouncements, "[") {
		return cninfoAnnouncementsPayload{}, errors.New("cninfo announcements field is not an array")
	}
	totalAnnouncement, hasTotalAnnouncement := envelope["totalAnnouncement"]
	totalRecordNum, hasTotalRecordNum := envelope["totalRecordNum"]
	if (!hasTotalAnnouncement || !validCninfoTotal(totalAnnouncement)) &&
		(!hasTotalRecordNum || !validCninfoTotal(totalRecordNum)) {
		return cninfoAnnouncementsPayload{}, errors.New("cninfo announcement response has no valid total")
	}
	var payload cninfoAnnouncementsPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return cninfoAnnouncementsPayload{}, err
	}
	return payload, nil
}

func validCninfoTotal(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var value any
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return false
	}
	switch typed := value.(type) {
	case json.Number:
		parsed, err := strconv.Atoi(typed.String())
		return err == nil && parsed >= 0
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(typed))
		return err == nil && parsed >= 0
	default:
		return false
	}
}

func cninfoAnnouncementFromRaw(raw map[string]any, symbol, market, orgID string) (StockV2Announcement, bool) {
	symbol = stockCodeOnly(symbol)
	title := cleanAnnouncementTitle(firstJSONText(raw, "announcementTitle", "title"))
	if symbol == "" || title == "" {
		return StockV2Announcement{}, false
	}
	if value := firstJSONText(raw, "orgId", "orgID", "orgid", "org_id"); value != "" {
		orgID = value
	}
	category := firstJSONText(raw, "category", "announcementTypeName", "categoryName")
	announcementID := firstJSONText(raw, "announcementId", "id")
	pdfURL := firstJSONText(raw, "adjunctUrl", "adjunctURL")
	if pdfURL != "" && !strings.HasPrefix(pdfURL, "http") {
		pdfURL = "https://static.cninfo.com.cn/" + strings.TrimLeft(pdfURL, "/")
	} else if parsed, err := url.Parse(pdfURL); err == nil &&
		strings.EqualFold(parsed.Scheme, "http") && strings.EqualFold(parsed.Hostname(), "static.cninfo.com.cn") {
		parsed.Scheme = "https"
		pdfURL = parsed.String()
	}
	publishedAt := parseCninfoTime(raw["announcementTime"])
	if publishedAt.IsZero() {
		publishedAt = parseCninfoDate(firstJSONText(raw, "announcementTime", "publishTime", "date"))
	}
	major, reason := classifyMajorAnnouncement(title, category)
	now := time.Now()
	return StockV2Announcement{
		ID:             generateID(),
		Source:         StockV2AnnouncementSourceCninfo,
		Symbol:         symbol,
		Market:         market,
		OrgID:          orgID,
		Title:          title,
		Category:       category,
		AnnouncementID: announcementID,
		PDFURL:         pdfURL,
		ContentHash:    announcementContentHash(symbol, title, category, pdfURL, publishedAt),
		Major:          major,
		MajorReason:    reason,
		PublishedAt:    publishedAt,
		FetchedAt:      now,
		CreatedAt:      now,
		UpdatedAt:      now,
	}, true
}

func cninfoInt(value any) int {
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case json.Number:
		parsed, _ := strconv.Atoi(typed.String())
		return parsed
	case string:
		parsed, _ := strconv.Atoi(strings.TrimSpace(typed))
		return parsed
	default:
		return 0
	}
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
		"中标", "行政处罚", "监管处罚", "纪律处分", "对外担保", "重大投资", "对外投资",
		"问询函", "监管函", "关注函", "问询回复", "回复问询", "监管回复",
		"董事长变动", "董事变动", "监事变动", "高管变动", "高级管理人员变动",
		"董事长辞职", "董事辞职", "监事辞职", "高级管理人员辞职", "总经理辞职",
		"聘任董事长", "聘任董事", "聘任监事", "聘任高级管理人员", "聘任总经理",
	}
	for _, rule := range rules {
		if strings.Contains(text, strings.ToLower(rule)) {
			return true, rule
		}
	}
	if strings.Contains(text, "回复") &&
		(strings.Contains(text, "问询") || strings.Contains(text, "监管")) {
		return true, "问询/监管回复"
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
