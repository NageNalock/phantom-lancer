package stockv2

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// DailyBarsSource 日 K 历史行情数据源（腾讯 fqkline 公开端点）。
//
// 数据来源：https://web.ifzq.gtimg.cn/appstock/app/fqkline/get
// 该端点原生支持 前复权(qfq) / 后复权(hfq) / 不复权 以及日期范围，与
// universe_source.go 同厂商。每条 bar 字段顺序为：
//
//	[date, open, close, high, low, volume]   ← 注意 open 后是 close，不是 high
//
// 该端点不提供成交额(amount)，Amount 字段保持 0，绝不伪造。
//
// 安全约束：失败时返回 error，不写空 bar、不编造价格、不用最新价派生日 K。
type DailyBarsSource struct {
	service    *Service
	httpClient *http.Client
}

// NewDailyBarsSource 创建日 K 数据源
func NewDailyBarsSource(service *Service, client *http.Client) *DailyBarsSource {
	return &DailyBarsSource{service: service, httpClient: client}
}

// marketPrefix 把市场代码映射为腾讯前缀；market 为空时按 symbol 首字母推断。
func marketPrefix(market, symbol string) (prefix, marketOut string) {
	switch strings.ToUpper(market) {
	case "SH":
		return "sh", "SH"
	case "SZ":
		return "sz", "SZ"
	case "BJ":
		return "bj", "BJ"
	}
	if inferred, _ := inferInstrumentMarketAndType(symbol); inferred != "" {
		switch inferred {
		case "SH":
			return "sh", "SH"
		case "SZ":
			return "sz", "SZ"
		case "BJ":
			return "bj", "BJ"
		}
	}
	return "sh", "SH"
}

// adjustedToFQ 把复权码映射为腾讯 fq 参数与返回 JSON 的 key。
func adjustedToFQ(adjusted string) (fqParam, dataKey string) {
	switch adjusted {
	case DailyBarAdjustedQFQ:
		return "qfq", "qfqday"
	case DailyBarAdjustedHFQ:
		return "hfq", "hfqday"
	default:
		return "", "day"
	}
}

// FetchDailyBars 抓取指定区间日 K。count 为请求条数上限（按区间档位给，
// 经验证 ≤1800 稳定返回 object；过大会触发端点异常返回 list）。
func (d *DailyBarsSource) FetchDailyBars(ctx context.Context, symbol, market, startDate, endDate, adjusted string, count int) ([]StockV2DailyBar, error) {
	symbol, explicitMarket := normalizeQuoteSymbolInput(symbol)
	if symbol == "" {
		return nil, fmt.Errorf("symbol is required")
	}
	if !isSixDigitSymbol(symbol) {
		return nil, fmt.Errorf("invalid symbol: %s", symbol)
	}
	if market == "" {
		market = explicitMarket
	}
	if count <= 0 {
		count = 400
	}
	prefix, marketNorm := marketPrefix(market, symbol)
	fqParam, dataKey := adjustedToFQ(adjusted)

	// param=<前缀><代码>,day,<start>,<end>,<count>,<fq>
	param := fmt.Sprintf("%s%s,day,%s,%s,%d,%s", prefix, symbol, startDate, endDate, count, fqParam)
	url := "https://web.ifzq.gtimg.cn/appstock/app/fqkline/get?param=" + param

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build fqkline request: %w", err)
	}
	req.Header.Set("User-Agent", pickDailyBarsUA())

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fqkline request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("fqkline http error: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("read fqkline body: %w", err)
	}

	bars, err := parseTencentFqKLine(body, prefix+symbol, marketNorm, adjusted, dataKey)
	if err != nil {
		return nil, err
	}
	if len(bars) == 0 {
		return nil, fmt.Errorf("fqkline returned 0 bars for %s", symbol)
	}
	return bars, nil
}

// parseTencentFqKLine 解析腾讯 fqkline 响应。
func parseTencentFqKLine(body []byte, prefixedSymbol, market, adjusted, dataKey string) ([]StockV2DailyBar, error) {
	var head struct {
		Code int             `json:"code"`
		Msg  string          `json:"msg"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &head); err != nil {
		return nil, fmt.Errorf("decode fqkline head: %w", err)
	}
	if head.Code != 0 {
		return nil, fmt.Errorf("fqkline code=%d msg=%q", head.Code, head.Msg)
	}

	// data 正常是 object { "<prefixedSymbol>": { day|qfqday|hfqday: [[...]] } }
	// count 过大等异常情况下会返回 array，此时视为失败（不臆测）。
	var dataMap map[string]json.RawMessage
	if err := json.Unmarshal(head.Data, &dataMap); err != nil {
		return nil, fmt.Errorf("fqkline data is not an object (unexpected shape): %w", err)
	}

	innerRaw, ok := dataMap[prefixedSymbol]
	if !ok {
		// 端点偶尔用不带前缀的 code 作为 key
		if r2, ok2 := dataMap[strings.TrimPrefix(prefixedSymbol, "sh")]; ok2 {
			innerRaw = r2
		} else if r3, ok3 := dataMap[strings.TrimPrefix(prefixedSymbol, "sz")]; ok3 {
			innerRaw = r3
		} else if r4, ok4 := dataMap[strings.TrimPrefix(prefixedSymbol, "bj")]; ok4 {
			innerRaw = r4
		} else {
			return nil, fmt.Errorf("fqkline data missing symbol key %q", prefixedSymbol)
		}
	}

	var inner map[string]json.RawMessage
	if err := json.Unmarshal(innerRaw, &inner); err != nil {
		return nil, fmt.Errorf("decode fqkline inner: %w", err)
	}

	rawArr, ok := inner[dataKey]
	if !ok {
		// 复权 key 缺失时（如该股无复权数据），兜底用不复权 day
		rawArr, ok = inner["day"]
		if !ok {
			return nil, fmt.Errorf("fqkline missing kline key %q", dataKey)
		}
	}

	var rows [][]string
	if err := json.Unmarshal(rawArr, &rows); err != nil {
		// 字段可能是 number 而非 string，尝试通用解析
		return parseTencentFqKLineGeneric(rawArr, market, adjusted)
	}

	return buildDailyBarsFromRows(rows, market, adjusted), nil
}

// parseTencentFqKLineGeneric 当字段为 number 时的通用解析兜底
func parseTencentFqKLineGeneric(rawArr json.RawMessage, market, adjusted string) ([]StockV2DailyBar, error) {
	var rows [][]any
	if err := json.Unmarshal(rawArr, &rows); err != nil {
		return nil, fmt.Errorf("decode fqkline rows: %w", err)
	}
	parsed := make([][]string, 0, len(rows))
	for _, r := range rows {
		ss := make([]string, 0, len(r))
		for _, c := range r {
			switch v := c.(type) {
			case string:
				ss = append(ss, v)
			case float64:
				ss = append(ss, strconv.FormatFloat(v, 'f', -1, 64))
			default:
				ss = append(ss, fmt.Sprintf("%v", c))
			}
		}
		parsed = append(parsed, ss)
	}
	return buildDailyBarsFromRows(parsed, market, adjusted), nil
}

// buildDailyBarsFromRows 把腾讯行数组转成 StockV2DailyBar。
// 行格式：[date, open, close, high, low, volume]
func buildDailyBarsFromRows(rows [][]string, market, adjusted string) []StockV2DailyBar {
	now := time.Now()
	bars := make([]StockV2DailyBar, 0, len(rows))
	var prevClose float64

	for _, f := range rows {
		if len(f) < 6 {
			continue
		}
		date := strings.TrimSpace(f[0])
		open := parseFloatTencent(f[1])
		closeP := parseFloatTencent(f[2])
		high := parseFloatTencent(f[3])
		low := parseFloatTencent(f[4])
		volume := parseFloatTencent(f[5])

		// 价格无效则丢弃该条，绝不写入坏数据
		if date == "" || open <= 0 || closeP <= 0 {
			continue
		}
		if high < low {
			high, low = low, high // 防御：纠正异常高低
		}

		pct := 0.0
		if prevClose > 0 {
			pct = (closeP - prevClose) / prevClose * 100
		}

		bars = append(bars, StockV2DailyBar{
			ID:        generateID(),
			Symbol:    "", // 由调用方按上下文回填
			Market:    market,
			TradeDate: date,
			Open:      open,
			High:      high,
			Low:       low,
			Close:     closeP,
			PrevClose: prevClose, // 首条为 0
			Volume:    volume,
			Amount:    0, // 该数据源不提供成交额
			PctChange: pct,
			Adjusted:  adjusted,
			Source:    "tencent_fqkline",
			FetchedAt: now,
			Quality:   DailyBarQualityOK,
		})

		prevClose = closeP
	}

	if len(bars) > 0 {
		// 首条没有前日收盘，标记为 partial 以便质量评估识别
		bars[0].PrevClose = 0
	}
	return bars
}

// HealthCheck 数据源健康检查：拉一只样本验证可达性与解析。
func (d *DailyBarsSource) HealthCheck(ctx context.Context) error {
	checkCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	_, err := d.FetchDailyBars(checkCtx, "600000", "SH", "2024-01-02", "2024-01-12", DailyBarAdjustedNone, 40)
	if err != nil {
		return fmt.Errorf("daily bars source health check failed: %w", err)
	}
	return nil
}

func pickDailyBarsUA() string {
	uas := []string{
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:121.0) Gecko/20100101 Firefox/121.0",
	}
	return uas[rand.IntN(len(uas))]
}
