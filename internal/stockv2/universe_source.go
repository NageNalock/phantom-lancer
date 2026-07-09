package stockv2

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"phantom-lancer/internal/safelog"

	"golang.org/x/text/encoding/simplifiedchinese"
)

// TencentQuoteResponse 腾讯行情响应结构
type TencentQuoteResponse struct {
	Symbol    string  `json:"symbol"`
	Name      string  `json:"name"`
	LastPrice float64 `json:"lastPrice"`
	PrevClose float64 `json:"prevClose"`
	OpenPrice float64 `json:"openPrice"`
	Volume    float64 `json:"volume"`
	High      float64 `json:"high"`
	Low       float64 `json:"low"`
	Amount    float64 `json:"amount"`
	Market    string  `json:"market"`
}

// UniverseDataSource 数据源管理
type UniverseDataSource struct {
	service    *Service
	httpClient *http.Client
}

type instrumentCodeMeta struct {
	Market         string
	InstrumentType string
}

// NewUniverseDataSource 创建数据源管理器
func NewUniverseDataSource(service *Service, client *http.Client) *UniverseDataSource {
	return &UniverseDataSource{
		service:    service,
		httpClient: client,
	}
}

// FetchStockUniverse 批量获取标的主数据
func (uds *UniverseDataSource) FetchStockUniverse(ctx context.Context, symbols []string) ([]StockV2Instrument, error) {
	if len(symbols) == 0 {
		return nil, nil
	}

	// 过滤和处理代码
	validSymbols, metaMap := uds.processSymbols(symbols)
	if len(validSymbols) == 0 {
		return nil, errors.New("no valid symbols to fetch")
	}

	// 使用腾讯接口批量获取数据
	instruments, err := uds.fetchTencentBatch(ctx, validSymbols, metaMap)
	if err != nil {
		return nil, fmt.Errorf("fetch tencent data failed: %w", err)
	}

	return instruments, nil
}

// processSymbols 处理标的代码，添加腾讯市场前缀。
func (uds *UniverseDataSource) processSymbols(symbols []string) ([]string, map[string]instrumentCodeMeta) {
	validSymbols := make([]string, 0, len(symbols))
	metaMap := make(map[string]instrumentCodeMeta)

	for _, sym := range symbols {
		sym = strings.TrimSpace(sym)
		if sym == "" {
			continue
		}
		symbol, explicitMarket := normalizeQuoteSymbolInput(sym)
		inferredMarket, instrumentType := inferInstrumentMarketAndType(symbol)
		market := inferredMarket
		if explicitMarket != "" {
			market = explicitMarket
		}
		if market == "" || !isSixDigitSymbol(symbol) {
			if uds != nil && uds.service != nil && uds.service.log != nil {
				uds.service.log.Warn("unknown market symbol", "symbol", sym)
			}
			continue
		}
		code := strings.ToLower(market) + symbol
		validSymbols = append(validSymbols, code)
		metaMap[code] = instrumentCodeMeta{
			Market:         market,
			InstrumentType: instrumentType,
		}
	}

	return validSymbols, metaMap
}

// fetchTencentBatch 批量获取腾讯数据
func (uds *UniverseDataSource) fetchTencentBatch(ctx context.Context, symbols []string, metaMap map[string]instrumentCodeMeta) ([]StockV2Instrument, error) {
	const batchSize = 80 // 腾讯接口推荐批量大小
	instruments := make([]StockV2Instrument, 0, len(symbols))

	total := len(symbols)
	for start := 0; start < total; start += batchSize {
		end := start + batchSize
		if end > total {
			end = total
		}
		batch := symbols[start:end]

		// 调用腾讯接口
		batchInstruments, err := uds.fetchTencentQuotes(ctx, batch, metaMap)
		if err != nil {
			uds.service.log.Error("fetch tencent quotes failed",
				"batch_start", start,
				"batch_end", end,
				"batch_size", len(batch),
				"error", safelog.Text(err.Error(), 300))
			continue
		}

		instruments = append(instruments, batchInstruments...)

		// 批间抖动，避免被腾讯风控误杀
		if end < total {
			if err := sleepJitter(ctx, 30*time.Millisecond, 50*time.Millisecond); err != nil {
				return instruments, err
			}
		}
	}

	if len(instruments) == 0 {
		return nil, errors.New("no instruments fetched")
	}

	return instruments, nil
}

// fetchTencentQuotes 获取腾讯实时行情
func (uds *UniverseDataSource) fetchTencentQuotes(ctx context.Context, tencentCodes []string, metaMap map[string]instrumentCodeMeta) ([]StockV2Instrument, error) {
	if len(tencentCodes) == 0 {
		return nil, nil
	}

	// 构造请求URL
	url := "https://qt.gtimg.cn/q=" + strings.Join(tencentCodes, ",")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request failed: %w", err)
	}

	// 设置随机 User-Agent
	userAgents := []string{
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/119.0.0.0 Safari/537.36",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
	}
	req.Header.Set("User-Agent", userAgents[rand.IntN(len(userAgents))])

	// 发送请求
	resp, err := uds.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	// 检查响应状态
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("http error: %d", resp.StatusCode)
	}

	// 读取响应体
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("read response failed: %w", err)
	}

	// 解析响应
	instruments, err := uds.parseTencentResponse(body, metaMap)
	if err != nil {
		return nil, fmt.Errorf("parse response failed: %w", err)
	}

	return instruments, nil
}

// parseTencentResponse 解析腾讯响应
func (uds *UniverseDataSource) parseTencentResponse(body []byte, metaMap map[string]instrumentCodeMeta) ([]StockV2Instrument, error) {
	// GBK 转 UTF-8
	utf8Body, err := simplifiedchinese.GBK.NewDecoder().Bytes(body)
	if err != nil && !utf8.Valid(body) {
		return nil, fmt.Errorf("gbk decode failed: %w", err)
	}
	text := string(utf8Body)

	instruments := make([]StockV2Instrument, 0, 100)
	now := time.Now()

	scanner := bufio.NewScanner(strings.NewReader(text))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		// 移除分号
		line = strings.TrimSuffix(line, ";")

		// 解析每行数据
		instrument, err := uds.parseTencentLine(line, metaMap)
		if err != nil {
			if uds != nil && uds.service != nil && uds.service.log != nil {
				uds.service.log.Warn("parse tencent line failed", "line", safelog.Text(line, 240), "error", safelog.Text(err.Error(), 240))
			}
			continue
		}

		if instrument != nil {
			instrument.CreatedAt = now
			instrument.UpdatedAt = now
			instruments = append(instruments, *instrument)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan failed: %w", err)
	}

	return instruments, nil
}

// parseTencentLine 解析单行腾讯数据
func (uds *UniverseDataSource) parseTencentLine(line string, metaMap map[string]instrumentCodeMeta) (*StockV2Instrument, error) {
	// 解析行格式：v_sh600000="1~浦发银行~600000~12.34~12.10~12.05~1000~500~500~...";\n
	eq := strings.Index(line, "=")
	if eq < 0 {
		return nil, fmt.Errorf("bad line format: %s", line)
	}

	payload := strings.Trim(line[eq+1:], `"`)
	if payload == "" {
		return nil, nil // 停牌/退市代码返回空串
	}

	fields := strings.Split(payload, "~")
	if len(fields) < 10 {
		return nil, fmt.Errorf("too few fields: %d", len(fields))
	}

	// 提取基本信息
	symbol := strings.TrimSpace(fields[2])
	name := strings.TrimSpace(fields[1])
	if symbol == "" || name == "" {
		return nil, fmt.Errorf("empty symbol or name")
	}

	// 获取市场与标的类型
	market := ""
	instrumentType := InstrumentTypeStock
	tencentKey := strings.TrimPrefix(line[:eq], "v_")
	if meta, ok := metaMap[tencentKey]; ok {
		market = meta.Market
		instrumentType = normalizeInstrumentType(meta.InstrumentType)
	} else {
		// 从代码推断市场
		market, instrumentType = inferInstrumentMarketAndType(symbol)
		if market == "" {
			return nil, fmt.Errorf("unknown market for symbol: %s", symbol)
		}
	}

	// 解析价格信息
	lastPrice := parseFloatTencent(fields[3])
	prevClose := parseFloatTencent(fields[4])

	// 如果价格无效，跳过
	if lastPrice <= 0 || prevClose <= 0 {
		return nil, nil
	}

	// 创建标的主数据对象
	instrument := StockV2Instrument{
		ID:             generateID(),
		Symbol:         symbol,
		Market:         market,
		InstrumentType: instrumentType,
		Name:           name,
		Status:         "unknown",
		LastUpdate:     time.Now(),
	}

	return &instrument, nil
}

// parseFloatTencent 解析腾讯价格字段
func parseFloatTencent(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" || s == "-" || s == "—" || s == "--" {
		return 0
	}

	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return v
}

// SleepJitter 带随机抖动的延迟
func sleepJitter(ctx context.Context, base, jitter time.Duration) error {
	duration := base + time.Duration(rand.Int64N(int64(jitter)))
	select {
	case <-time.After(duration):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// MockDataForTest 用于测试的模拟数据
func (uds *UniverseDataSource) MockDataForTest() []StockV2Instrument {
	return []StockV2Instrument{
		{
			ID:             generateID(),
			Symbol:         "000001",
			Market:         "SZ",
			InstrumentType: InstrumentTypeStock,
			Name:           "平安银行",
			Industry:       "银行",
			Sector:         "金融",
			Status:         "unknown",
			LastUpdate:     time.Now(),
		},
		{
			ID:             generateID(),
			Symbol:         "000002",
			Market:         "SZ",
			InstrumentType: InstrumentTypeStock,
			Name:           "万科A",
			Industry:       "房地产",
			Sector:         "地产",
			Status:         "unknown",
			LastUpdate:     time.Now(),
		},
		{
			ID:             generateID(),
			Symbol:         "600000",
			Market:         "SH",
			InstrumentType: InstrumentTypeStock,
			Name:           "浦发银行",
			Industry:       "银行",
			Sector:         "金融",
			Status:         "unknown",
			LastUpdate:     time.Now(),
		},
		{
			ID:             generateID(),
			Symbol:         "600036",
			Market:         "SH",
			InstrumentType: InstrumentTypeStock,
			Name:           "招商银行",
			Industry:       "银行",
			Sector:         "金融",
			Status:         "unknown",
			LastUpdate:     time.Now(),
		},
	}
}

// HealthCheck 数据源健康检查
func (uds *UniverseDataSource) HealthCheck(ctx context.Context) error {
	// 简单的健康检查：尝试获取少量股票数据
	testSymbols := []string{"000001", "600000"}
	_, err := uds.FetchStockUniverse(ctx, testSymbols)
	if err != nil {
		return fmt.Errorf("tencent data source health check failed: %w", err)
	}
	return nil
}

// GetDefaultSymbols 获取默认标的代码列表。
// 优先从新浪行情接口拉取 A 股股票 + ETF/LOF 场内基金，失败时回退到核心龙头样本。
func (uds *UniverseDataSource) GetDefaultSymbols() []string {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	symbols, err := uds.fetchSinaUniverseSymbols(ctx)
	if err == nil && len(symbols) > 0 {
		uds.service.log.Info("fetched full A-share universe from sina", "count", len(symbols))
		return symbols
	}

	uds.service.log.Warn("fallback to sample universe", "error", safelog.Text(err.Error(), 300), "sample_count", len(uds.sampleUniverseSymbols()))
	return uds.sampleUniverseSymbols()
}

// sampleUniverseSymbols 核心龙头样本（仅作降级兜底）
func (uds *UniverseDataSource) sampleUniverseSymbols() []string {
	return []string{
		// 沪市主板
		"600000", "600036", "600016", "600030", "600519", "601318",
		"601398", "601288", "601988", "600028", "601857", "600900",
		"600276", "600887", "600690", "600585", "601012", "600089",
		"601138", "600050", "600941", "601728", "601390", "601668",
		// 深市主板 / 中小板
		"000001", "000002", "000858", "000333", "002415", "002142",
		"000651", "000725", "000063", "002352", "000538", "000568",
		"000799", "000895", "002304", "002230", "002049", "002714",
		// 创业板
		"300750", "300033", "300059", "300015", "300760", "300124",
		"300274", "300751", "300770", "300014", "300433", "300450",
		// 北交所
		"839008", "830799", "834765", "835179", "833819",
		// 场内基金
		"510300", "510500", "159915", "159949", "161725",
	}
}

// fetchSinaUniverseSymbols 从新浪行情接口拉取 A 股与主要场内基金代码列表，自动分页。
func (uds *UniverseDataSource) fetchSinaUniverseSymbols(ctx context.Context) ([]string, error) {
	nodes := []string{"sh_a", "sz_a", "bj_a", "etf_hq_fund", "lof_hq_fund"}
	var all []string
	seen := make(map[string]struct{})
	const pageSize = 100 // 新浪单页上限
	const maxPages = 80  // 防护：最多 80 页

	for i, node := range nodes {
		if i > 0 {
			if err := sleepJitter(ctx, 100*time.Millisecond, 200*time.Millisecond); err != nil {
				return all, err
			}
		}

		nodeCount := 0
		for page := 1; page <= maxPages; page++ {
			url := "https://money.finance.sina.com.cn/quotes_service/api/json_v2.php/Market_Center.getHQNodeData" +
				"?page=" + strconv.Itoa(page) +
				"&num=" + strconv.Itoa(pageSize) +
				"&sort=symbol&asc=1&node=" + node +
				"&symbol=&_s_r_a=page" +
				"&_=" + strconv.FormatInt(time.Now().UnixMilli()+int64(page)*137, 10)

			req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
			if err != nil {
				uds.service.log.Warn("sina universe request build failed", "node", node, "page", page, "error", safelog.Text(err.Error(), 240))
				break
			}
			req.Header.Set("Referer", "https://finance.sina.com.cn/")
			req.Header.Set("User-Agent", pickSinaUA(page+i))

			resp, err := uds.httpClient.Do(req)
			if err != nil {
				uds.service.log.Warn("sina universe fetch failed", "node", node, "page", page, "error", safelog.Text(err.Error(), 240))
				break
			}
			body, err := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024))
			resp.Body.Close()
			if err != nil {
				uds.service.log.Warn("sina universe read failed", "node", node, "page", page, "error", safelog.Text(err.Error(), 240))
				break
			}
			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				uds.service.log.Warn("sina universe http error", "node", node, "page", page, "status", resp.StatusCode)
				break
			}

			symbols := parseSinaSymbolList(body)
			if len(symbols) == 0 {
				break // 空页 = 已经拉完
			}
			for _, symbol := range symbols {
				if _, ok := seen[symbol]; ok {
					continue
				}
				seen[symbol] = struct{}{}
				all = append(all, symbol)
				nodeCount++
			}

			if len(symbols) < pageSize {
				break // 不满一页 = 最后一页
			}

			// 页间抖动
			if err := sleepJitter(ctx, 50*time.Millisecond, 100*time.Millisecond); err != nil {
				return all, err
			}
		}

		uds.service.log.Info("sina universe node fetched", "node", node, "count", nodeCount)
	}

	if len(all) == 0 {
		return nil, errors.New("sina universe returned 0 symbols")
	}
	return all, nil
}

// parseSinaSymbolList 从新浪伪 JSON 中提取标的代码列表
func parseSinaSymbolList(body []byte) []string {
	s := string(body)
	// 去掉 UTF-8 BOM (EF BB BF)
	if len(s) >= 3 && s[0] == 0xEF && s[1] == 0xBB && s[2] == 0xBF {
		s = s[3:]
	}
	s = strings.TrimSpace(s)
	// 去掉可能的 var XXX= 前缀和尾部分号
	if eq := strings.Index(s, "="); eq >= 0 && !strings.ContainsAny(s[:eq], "[]{}") {
		s = strings.TrimSpace(s[eq+1:])
	}
	s = strings.TrimSuffix(s, ";")
	s = strings.TrimSpace(s)
	if s == "" || s == "null" || s == "[]" {
		return nil
	}

	// 简单提取：找所有 "code":"XXXXXX" 或 code:"XXXXXX"
	var symbols []string
	// 正则可能太重，直接用字符串扫描
	text := s
	for {
		// 找 code: 或 "code":
		idx := strings.Index(text, `"code"`)
		if idx < 0 {
			idx = strings.Index(text, `code:`)
		}
		if idx < 0 {
			break
		}
		// 跳到值的起始位置
		rest := text[idx:]
		// 找冒号
		colon := strings.IndexByte(rest, ':')
		if colon < 0 {
			break
		}
		rest = rest[colon+1:]
		rest = strings.TrimLeft(rest, " \t")
		if strings.HasPrefix(rest, `"`) {
			rest = rest[1:]
			end := strings.IndexByte(rest, '"')
			if end > 0 && end <= 10 {
				code := rest[:end]
				if len(code) == 6 {
					symbols = append(symbols, code)
				}
			}
		}
		// 移动指针
		text = text[idx+5:]
	}
	return symbols
}

func pickSinaUA(seed int) string {
	uas := []string{
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:121.0) Gecko/20100101 Firefox/121.0",
	}
	return uas[seed%len(uas)]
}
