package stock

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"phantom-lancer/internal/storage"

	pinyinlib "github.com/mozillazg/go-pinyin"
)

// MaintenanceMode 控制 refreshAStockUniverse 是否做频率门控。
type MaintenanceMode int

const (
	// MaintenanceModeManual 手动模式：忽略最近运行时间，直接执行。
	MaintenanceModeManual MaintenanceMode = iota
	// MaintenanceModeDaily  每日模式：< 20h 内成功跑过则跳过。
	MaintenanceModeDaily
)

// eastmoneyUniverseFS 东方财富全 A 股市场 fs 编码（深主/深创/沪主/沪科/北交）。
const eastmoneyUniverseFS = "m:0+t:6,m:0+t:80,m:1+t:2,m:1+t:23,m:0+t:81+s:2048"

// eastmoneyClistFields 主源字段：f12=symbol, f13=market, f14=name, f18=status_code, f100=industry_id
// 我们只取主数据必须字段，体积小。
const eastmoneyClistFields = "f12,f13,f14,f18,f100,f102"

// universeUserAgents 3 套轮换 UA，尽量避免被东财风控命中。
var universeUserAgents = []string{
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 14_6; rv:129.0) Gecko/20100101 Firefox/129.0",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36",
	"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36",
}

// universeInstrument 内部中间结构，加上 remoteMarket（f13 原始值）便于后续处理。
type universeInstrument struct {
	Symbol       string
	RemoteMarket int // 东财 f13: 0=SZ 1=SH 2=BJ
	Name         string
	ListingDate  string // f18 可能为空，或为 0
	IndustryCode string // f100 原始字符串
}

func (s *Service) RefreshAStockUniverse(ctx context.Context, mode MaintenanceMode, requestedBy string) (DataTaskResult, error) {
	requestedBy = defaultString(requestedBy, "system")
	if mode == MaintenanceModeDaily {
		if last := s.store.LastTaskCompletedAt(ctx, "universe_refresh"); !last.IsZero() && s.now().Sub(last) < 20*time.Hour {
			return DataTaskResult{Notes: []string{"A 股全量主数据刷新已在 20 小时内完成，本轮跳过"}}, nil
		}
	}
	raw, fetchNotes, fetchErr := s.fetchEastmoneyUniverse(ctx)
	source := "eastmoney_universe"
	fullSource := true
	if fetchErr != nil {
		fetchNotes = append(fetchNotes, "eastmoney_universe: "+fetchErr.Error())
		eastmoneyNotes := append([]string{}, fetchNotes...)
		raw, fetchNotes, fetchErr = s.fetchSinaUniverse(ctx)
		fetchNotes = append(eastmoneyNotes, fetchNotes...)
		source = "sina_universe"
		fullSource = false
	}
	src, err := s.ensureDataSource(ctx, source, "market_data")
	if err != nil {
		return DataTaskResult{}, err
	}
	if fetchErr != nil {
		summary := limitText(strings.Join(fetchNotes, "; "), stockQuoteFailureSummaryMaxLength)
		_, _ = s.recordDataSourceFailure(ctx, src, summary)
		task, taskErr := s.store.CreateStockDataTask(ctx, storage.StockDataTask{
			TaskType:       "universe_refresh",
			Source:         src.Source,
			Status:         "failed",
			RequestedBy:    requestedBy,
			InputJSON:      mustJSON(map[string]any{"mode": mode, "source": src.Source}),
			ResultJSON:     mustJSON(map[string]any{"saved": 0, "failed": 1}),
			FailedCount:    1,
			FailureSummary: summary,
			NextRunAt:      s.now().UTC().Add(quoteProviderBackoff(src.RateLimitSeconds, src.ConsecutiveFailures+1)).Format(time.RFC3339Nano),
		})
		if taskErr != nil {
			return DataTaskResult{}, taskErr
		}
		return DataTaskResult{Task: task, Notes: fetchNotes}, fetchErr
	}
	industryMap, industryNotes := s.fetchIndustryMap(ctx)
	fetchNotes = append(fetchNotes, industryNotes...)
	items := convertInstruments(raw, industryMap)
	for i := range items {
		items[i].Source = src.Source
		if !fullSource {
			items[i].Quality = "partial"
		}
	}
	saved, upsertNotes := s.store.BatchUpsertInstruments(ctx, items)
	notes := append(fetchNotes, upsertNotes...)
	failed := len(upsertNotes)
	local, err := s.store.AllInstrumentSymbols(ctx)
	if err != nil {
		return DataTaskResult{}, err
	}
	remote := make(map[string]bool, len(items))
	for _, item := range items {
		remote[item.Symbol] = true
	}
	orphans := make([]string, 0)
	for symbol := range local {
		if !remote[symbol] {
			orphans = append(orphans, symbol)
		}
	}
	delisted := 0
	if fullSource && len(items) >= 1000 {
		delisted, err = s.store.MarkInstrumentsDelisted(ctx, orphans)
		if err != nil {
			return DataTaskResult{}, err
		}
	} else if len(orphans) > 0 {
		notes = append(notes, "兜底或部分主数据刷新未执行退市软标，避免误改历史主数据")
	}
	status := taskStatus(saved, failed)
	if len(fetchNotes) > 0 && status == "completed" {
		status = "degraded"
	}
	summary := limitText(strings.Join(notes, "; "), stockQuoteFailureSummaryMaxLength)
	task, err := s.store.CreateStockDataTask(ctx, storage.StockDataTask{
		TaskType:       "universe_refresh",
		Source:         src.Source,
		Status:         status,
		RequestedBy:    requestedBy,
		InputJSON:      mustJSON(map[string]any{"mode": mode, "source": src.Source}),
		ResultJSON:     mustJSON(map[string]any{"fetched": len(raw), "saved": saved, "delisted": delisted, "failed": failed, "notes": notes}),
		ProcessedCount: saved,
		FailedCount:    failed,
		FailureSummary: failureSummary(len(notes), summary),
	})
	if err != nil {
		return DataTaskResult{}, err
	}
	nextRun := s.now().UTC().Add(20 * time.Hour)
	_, _ = s.store.UpdateStockDataSourceHealth(ctx, storage.StockDataSource{
		Source:              src.Source,
		Status:              "available",
		Quality:             mapQuoteRefreshQuality(status),
		LastIngestedAt:      task.CompletedAt,
		NextAllowedAt:       nextRun.Format(time.RFC3339Nano),
		ConsecutiveFailures: 0,
		FailureSummary:      summary,
	})
	return DataTaskResult{Task: task, Instruments: items, Notes: notes}, nil
}

// pinyinArgs 通用拼音参数（首字母 + 无声调，A 股公司名不会有 ü 等特殊字符）。
var pinyinArgs = pinyinlib.NewArgs()

func init() {
	pinyinArgs.Style = pinyinlib.Normal // guì → gui（带声调不行，改为首字母模式时用 FirstLetter）
}

// pyInitials 返回名称的首字母全大写（贵州茅台 → GZMT）。
func pyInitials(name string) string {
	args := pinyinlib.NewArgs()
	args.Style = pinyinlib.FirstLetter
	segs := pinyinlib.LazyPinyin(name, args)
	return strings.ToUpper(strings.Join(segs, ""))
}

// pyFull 返回拼音全拼（小写，无空格无分隔，贵州茅台 → guizhimaotai）。
func pyFull(name string) string {
	args := pinyinlib.NewArgs()
	args.Style = pinyinlib.Normal
	segs := pinyinlib.LazyPinyin(name, args)
	return strings.ToLower(strings.Join(segs, ""))
}

// fetchEastmoneyUniverse 从东方财富 clist/get 分页拉全市场代码列表。
// 返回 items 和错误笔记。若主源完全不可用则 err != nil。
func (s *Service) fetchEastmoneyUniverse(ctx context.Context) ([]universeInstrument, []string, error) {
	const pageSize = 500
	var all []universeInstrument
	var notes []string

	// 先拿第 1 页确定 total（东财接口 total 字段位于 data.total）
	firstPage, total, err := s.fetchEastmoneyClistPage(ctx, 1, pageSize)
	if err != nil {
		return nil, notes, fmt.Errorf("eastmoney clist page 1: %w", err)
	}
	all = append(all, firstPage...)
	if total <= pageSize {
		return all, notes, nil
	}
	// 向上取整算页数，最高到 20 页封顶（防 total 异常值打挂）
	pages := (total + pageSize - 1) / pageSize
	if pages > 20 {
		pages = 20
	}
	for p := 2; p <= pages; p++ {
		// 100ms sleep 防反爬
		select {
		case <-ctx.Done():
			return all, notes, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
		page, _, perr := s.fetchEastmoneyClistPage(ctx, p, pageSize)
		if perr != nil {
			notes = append(notes, fmt.Sprintf("page %d: %v", p, perr))
			continue
		}
		all = append(all, page...)
		if len(page) < pageSize {
			break
		}
	}
	if len(all) == 0 {
		return nil, notes, fmt.Errorf("eastmoney clist 返回 0 条股票; notes=%s", strings.Join(notes, "; "))
	}
	return all, notes, nil
}

// fetchEastmoneyClistPage 拉单页，解析出 items 和东财返回的 total。
func (s *Service) fetchEastmoneyClistPage(ctx context.Context, pageNum, pageSize int) ([]universeInstrument, int, error) {
	url := "https://push2.eastmoney.com/api/qt/clist/get" +
		"?pn=" + strconv.Itoa(pageNum) +
		"&pz=" + strconv.Itoa(pageSize) +
		"&po=1&np=1&fltt=2&invt=2" +
		"&fid=f3" +
		"&fs=" + eastmoneyUniverseFS +
		"&fields=" + eastmoneyClistFields
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Referer", "https://quote.eastmoney.com/")
	req.Header.Set("User-Agent", universeUserAgents[(pageNum-1)%len(universeUserAgents)])
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, 0, fmt.Errorf("status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		return nil, 0, err
	}
	var payload struct {
		Data struct {
			Total int `json:"total"`
			Diff  []struct {
				Symbol       string `json:"f12"`
				MarketCode   int    `json:"f13"`
				Name         string `json:"f14"`
				ListingDate  any    `json:"f18"` // 可能是 int (0), 空串, 或 "2001-08-27"
				IndustryCode string `json:"f100"`
			} `json:"diff"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, 0, fmt.Errorf("unmarshal: %w (first 200 bytes: %s)", err, truncateBytes(body, 200))
	}
	items := make([]universeInstrument, 0, len(payload.Data.Diff))
	for _, d := range payload.Data.Diff {
		if d.Symbol == "" || d.Name == "" {
			continue
		}
		// 过滤掉东财内部占位条目（名称是 "-" 等）
		if strings.TrimSpace(d.Name) == "-" || d.Name == "股票代码" {
			continue
		}
		listing := ""
		switch v := d.ListingDate.(type) {
		case string:
			listing = v
		case float64:
			if v > 1e8 { // unixtime 秒
				listing = time.Unix(int64(v), 0).Format("2006-01-02")
			}
		case int:
			if v > 1e8 {
				listing = time.Unix(int64(v), 0).Format("2006-01-02")
			}
		}
		// 东财 f18 很多新股返回 ""，正常忽略即可，status 也不在这列里体现
		items = append(items, universeInstrument{
			Symbol:       d.Symbol,
			RemoteMarket: d.MarketCode,
			Name:         d.Name,
			ListingDate:  listing,
			IndustryCode: d.IndustryCode,
		})
	}
	return items, payload.Data.Total, nil
}

// fetchSinaUniverse 新浪兜底：按 sh/sz/bj 三个市场节点拉代码，失败时只返回能拿到的部分。
// 接口：money.finance.sina.com.cn/quotes_service/api/json_v2.php/Market_Center.getHQNodeData
func (s *Service) fetchSinaUniverse(ctx context.Context) ([]universeInstrument, []string, error) {
	nodes := []struct {
		pageFlag string // 对应节点
		market   int    // 映射成东财风格 0=SZ 1=SH 2=BJ
	}{
		{"sh_a", 1},
		{"sz_a", 0},
		{"bj_a", 2},
	}
	var all []universeInstrument
	var notes []string
	for _, n := range nodes {
		const pageSize = 2000
		url := "https://money.finance.sina.com.cn/quotes_service/api/json_v2.php/Market_Center.getHQNodeData" +
			"?page=1&num=" + strconv.Itoa(pageSize) +
			"&sort=symbol&asc=1&node=" + n.pageFlag +
			"&symbol=&_s_r_a=page"
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			notes = append(notes, fmt.Sprintf("%s: %v", n.pageFlag, err))
			continue
		}
		req.Header.Set("Referer", "https://finance.sina.com.cn/")
		req.Header.Set("User-Agent", stockQuoteUserAgent)
		resp, err := s.client.Do(req)
		if err != nil {
			notes = append(notes, fmt.Sprintf("%s: %v", n.pageFlag, err))
			continue
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
		resp.Body.Close()
		if err != nil {
			notes = append(notes, fmt.Sprintf("%s: read: %v", n.pageFlag, err))
			continue
		}
		// 新浪返回的是 JS 伪 JSON（字段不加引号），宽松解析的话先做键加引号：
		fixed := fixSinaPseudoJSON(body)
		var rows []struct {
			Symbol      string `json:"code"`
			Name        string `json:"name"`
			TradeStatus string `json:"trade"` // 1=可交易 0=停牌（新浪接口）
		}
		if err := json.Unmarshal(fixed, &rows); err != nil {
			notes = append(notes, fmt.Sprintf("%s: unmarshal: %v", n.pageFlag, err))
			continue
		}
		for _, r := range rows {
			if r.Symbol == "" || r.Name == "" {
				continue
			}
			all = append(all, universeInstrument{
				Symbol:       r.Symbol,
				RemoteMarket: n.market,
				Name:         r.Name,
			})
		}
		select {
		case <-ctx.Done():
			return all, notes, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	if len(all) == 0 {
		return nil, notes, fmt.Errorf("sina universe 返回 0 条: %s", strings.Join(notes, "; "))
	}
	return all, notes, nil
}

// fixSinaPseudoJSON 把新浪接口返回的 "var x = [{code:\"a\",name:\"b\"}];" 风格伪 JSON 转成合法 JSON。
// 只做必要的键名加引号 + 去除 var x = 前缀 + 尾分号。
func fixSinaPseudoJSON(body []byte) []byte {
	s := strings.TrimSpace(string(body))
	// 去除可能的 "var XXX=" 前缀（接口 2 种返回形式都要兼容）
	if eq := strings.Index(s, "="); eq >= 0 && strings.HasPrefix(strings.TrimSpace(s[:eq]), "var ") {
		s = strings.TrimSpace(s[eq+1:])
	}
	s = strings.TrimSuffix(s, ";")
	s = strings.TrimSpace(s)
	if s == "" || s == "null" {
		return []byte("[]")
	}
	// 简单正则：把 { 和 , 后的 标识符: 形式加上双引号
	var b strings.Builder
	b.Grow(len(s) + 32)
	sr := []rune(s)
	for i := 0; i < len(sr); i++ {
		c := sr[i]
		switch c {
		case '{', ',':
			b.WriteRune(c)
			// 跳过空白，找到标识符起点
			j := i + 1
			for j < len(sr) && (sr[j] == ' ' || sr[j] == '\t' || sr[j] == '\n' || sr[j] == '\r') {
				b.WriteRune(sr[j])
				j++
			}
			// 若下一个非空白不是双引号，则是未加引号的标识符，一直读直到 ':'
			if j < len(sr) && sr[j] != '"' {
				start := j
				for j < len(sr) && sr[j] != ':' {
					j++
				}
				key := strings.TrimSpace(string(sr[start:j]))
				b.WriteString(`"` + key + `":`)
				i = j // 跳过 ':'
				continue
			}
		default:
			b.WriteRune(c)
		}
	}
	return []byte(b.String())
}

// marketFromRemote 把东财 f13 市场码 / 新浪映射码 转成内部 SH/SZ/BJ。
func marketFromRemote(remote int, symbol string) string {
	switch remote {
	case 0:
		return "SZ"
	case 1:
		return "SH"
	case 2:
		return "BJ"
	}
	// 兜底：按代码前缀判断（symbol 没带 market 前缀的情况）
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	switch {
	case strings.HasPrefix(symbol, "6") || strings.HasPrefix(symbol, "9"):
		return "SH"
	case strings.HasPrefix(symbol, "0") || strings.HasPrefix(symbol, "3"):
		return "SZ"
	case strings.HasPrefix(symbol, "8") || strings.HasPrefix(symbol, "4"):
		return "BJ"
	}
	return "SH"
}

// convertInstruments 把中间结构 → StockInstrument，填入拼音 + 行业名映射 + tradable 状态。
// industryLookup 由东财 slist/get 返回的行业编码→中文名称映射表。
func convertInstruments(raw []universeInstrument, industryLookup map[string]string) []storage.StockInstrument {
	out := make([]storage.StockInstrument, 0, len(raw))
	for _, r := range raw {
		sym := strings.TrimSpace(r.Symbol)
		if sym == "" {
			continue
		}
		mkt := marketFromRemote(r.RemoteMarket, sym)
		name := strings.TrimSpace(r.Name)
		industry := ""
		if r.IndustryCode != "" {
			industry = industryLookup[r.IndustryCode]
		}
		out = append(out, storage.StockInstrument{
			Symbol:      sym,
			Market:      mkt,
			Name:        name,
			Status:      "tradable", // 东财 clist 是活跃代码快照；healthTicker 会在之后刷新停牌
			Industry:    industry,
			ListingDate: r.ListingDate,
			Source:      "eastmoney_universe",
			Quality:     "fresh",
			PY:          pyInitials(name),
			PYFull:      pyFull(name),
		})
	}
	return out
}

// fetchIndustryMap 从东财行业板块接口补 industry 中文名（可选；失败不影响主流程）。
// 简化实现：走 push2.eastmoney.com/api/qt/clist/get + fs=m:90+t:2（行业板块）拿行业列表。
func (s *Service) fetchIndustryMap(ctx context.Context) (map[string]string, []string) {
	m := map[string]string{}
	var notes []string
	// 东财行业编码由 f100 给，但 clist 查行业板块列表拿不到编码对应关系，
	// 所以这里不做编码映射——改为在刷新全量时，对每只股票单独查所属行业成本太高，
	// 故行业名留空（由行业 slist/get 或健康探测期间离线异步补），
	// 并在后续健康探测任务里如果 industry 为空就尝试单独补。
	// TODO(stock-masterdata-v3): 接入 push2.eastmoney.com/api/qt/slist/get 行业概念补充
	return m, notes
}

// truncateBytes 截断 body 到 n 字节，用于日志避免过大。
func truncateBytes(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "..."
}
