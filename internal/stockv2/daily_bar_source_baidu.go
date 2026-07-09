package stockv2

import (
	"bytes"
	"context"
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

const baiduDailyBarsDefaultMinInterval = 750 * time.Millisecond

var errBaiduDailyBarsAccessDenied = errors.New("baidu daily bars access denied")

// BaiduDailyBarsSource 百度财经日 K 数据源。
//
// 数据来源：https://finance.pae.baidu.com/selfselect/getstockquotation
// 该端点返回全量历史日 K，字段包括：
//
//	timestamp, time, open, close, volume, high, low, amount, range, ratio,
//	turnoverratio, preClose, ma5avgprice, ma5volume, ma10avgprice, ma10volume,
//	ma20avgprice, ma20volume
//
// 相比腾讯 fqkline，百度提供了 amount（成交额）和 turnoverratio（换手率），
// 但仅返回不复权数据。全量返回 ~2000 条/250KB，upsert 幂等写入。
type BaiduDailyBarsSource struct {
	httpClient    *http.Client
	throttleMu    sync.Mutex
	lastRequestAt time.Time
	minInterval   time.Duration
}

// NewBaiduDailyBarsSource 创建百度日 K 数据源
func NewBaiduDailyBarsSource(client *http.Client) *BaiduDailyBarsSource {
	return &BaiduDailyBarsSource{
		httpClient:  client,
		minInterval: baiduDailyBarsDefaultMinInterval,
	}
}

// FetchDailyBars 抓取指定标的全量日 K 数据。
// symbol 为 6 位股票代码（如 "002457"），market 为 SH/SZ/BJ。
// instrumentType 用于区分股票和 ETF（使用不同的 isFutures/isStock 参数）。
func (b *BaiduDailyBarsSource) FetchDailyBars(ctx context.Context, symbol, market, instrumentType string) ([]StockV2DailyBar, error) {
	symbol = strings.TrimSpace(symbol)
	if !isSixDigitSymbol(symbol) {
		return nil, fmt.Errorf("invalid symbol for baidu daily bars: %s", symbol)
	}

	isStock := "true"
	isFutures := "false"
	typ := normalizeInstrumentType(instrumentType)
	if typ == InstrumentTypeExchangeFund || looksLikeExchangeFund("") {
		isStock = "false"
		isFutures = "true"
	}
	_ = market // 百度接口不需要 market 参数，code 已足够区分

	params := url.Values{}
	params.Set("all", "1")
	params.Set("isIndex", "false")
	params.Set("isBk", "false")
	params.Set("isBlock", "false")
	params.Set("isFutures", isFutures)
	params.Set("isStock", isStock)
	params.Set("newFormat", "1")
	params.Set("group", "quotation_kline_ab")
	params.Set("finClientType", "pc")
	params.Set("code", symbol)
	params.Set("ktype", "1")

	reqURL := "https://finance.pae.baidu.com/selfselect/getstockquotation?" + params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build baidu request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")

	client := b.httpClient
	if client == nil {
		client = http.DefaultClient
	}
	if err := b.waitRequestTurn(ctx); err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("baidu request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("baidu http error: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 5*1024*1024)) // 单只股票 ~250KB，5MB 上限足够
	if err != nil {
		return nil, fmt.Errorf("read baidu body: %w", err)
	}

	bars, err := parseBaiduMarketData(body, symbol, market)
	if err != nil {
		return nil, fmt.Errorf("parse baidu response: %w", err)
	}
	if len(bars) == 0 {
		return nil, fmt.Errorf("baidu returned 0 bars for %s", symbol)
	}
	return bars, nil
}

// baiduAPIResponse 百度财经 API 响应结构
type baiduAPIResponse struct {
	QueryID    string          `json:"QueryID"`
	ResultCode string          `json:"ResultCode"`
	Result     json.RawMessage `json:"Result"`
}

type baiduMarketDataResult struct {
	NewMarketData struct {
		Headers    []string `json:"headers"`
		Keys       []string `json:"keys"`
		MarketData string   `json:"marketData"` // 分号分隔行，逗号分隔字段
	} `json:"newMarketData"`
}

// parseBaiduMarketData 解析百度返回的 marketData 字符串
func parseBaiduMarketData(body []byte, symbol, market string) ([]StockV2DailyBar, error) {
	var resp baiduAPIResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal baidu response: %w", err)
	}
	if resp.ResultCode != "0" {
		if resp.ResultCode == "403" {
			return nil, fmt.Errorf("%w: ResultCode=%s", errBaiduDailyBarsAccessDenied, resp.ResultCode)
		}
		return nil, fmt.Errorf("baidu ResultCode=%s", resp.ResultCode)
	}

	md, err := extractBaiduMarketData(resp.Result)
	if err != nil {
		return nil, err
	}
	if md == "" {
		return nil, fmt.Errorf("empty marketData")
	}

	// 字段顺序（来自 keys）: timestamp, time, open, close, volume, high, low, amount,
	// range, ratio, turnoverratio, preClose, ma5avgprice, ma5volume, ...
	now := time.Now()
	bars := make([]StockV2DailyBar, 0, 2048)
	rows := strings.Split(md, ";")
	for _, row := range rows {
		row = strings.TrimSpace(row)
		if row == "" {
			continue
		}
		parts := strings.Split(row, ",")
		if len(parts) < 12 {
			continue
		}

		date := strings.TrimSpace(parts[1]) // time 字段，格式 "2026-07-09"
		open := parseFloatBaidu(parts[2])
		closeP := parseFloatBaidu(parts[3])
		volume := parseFloatBaidu(parts[4])
		high := parseFloatBaidu(parts[5])
		low := parseFloatBaidu(parts[6])
		amount := parseFloatBaidu(parts[7])
		// parts[8] = range (涨跌额), parts[9] = ratio (涨跌幅%)
		turnoverRate := parseFloatBaidu(parts[10])
		preClose := parseFloatBaidu(parts[11])

		// 价格无效则丢弃，绝不写入坏数据
		if date == "" || open <= 0 || closeP <= 0 {
			continue
		}
		if high < low {
			high, low = low, high // 防御：纠正异常高低
		}

		pct := 0.0
		if preClose > 0 {
			pct = (closeP - preClose) / preClose * 100
		}

		bars = append(bars, StockV2DailyBar{
			ID:           generateID(),
			Symbol:       "", // 由调用方按上下文回填
			Market:       market,
			TradeDate:    date,
			Open:         open,
			High:         high,
			Low:          low,
			Close:        closeP,
			PrevClose:    preClose,
			Volume:       volume,
			Amount:       amount,
			PctChange:    pct,
			TurnoverRate: turnoverRate,
			Adjusted:     DailyBarAdjustedNone, // 百度仅提供不复权
			Source:       "baidu_kline",
			FetchedAt:    now,
			Quality:      DailyBarQualityOK,
		})
	}

	return bars, nil
}

func (b *BaiduDailyBarsSource) waitRequestTurn(ctx context.Context) error {
	interval := b.minInterval
	if interval <= 0 {
		interval = baiduDailyBarsDefaultMinInterval
	}

	b.throttleMu.Lock()
	defer b.throttleMu.Unlock()

	if !b.lastRequestAt.IsZero() {
		wait := interval - time.Since(b.lastRequestAt)
		if wait > 0 {
			timer := time.NewTimer(wait)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			}
		}
	}
	b.lastRequestAt = time.Now()
	return nil
}

func extractBaiduMarketData(raw json.RawMessage) (string, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return "", fmt.Errorf("empty Result")
	}

	var results []baiduMarketDataResult
	switch raw[0] {
	case '{':
		var result baiduMarketDataResult
		if err := json.Unmarshal(raw, &result); err != nil {
			return "", fmt.Errorf("unmarshal baidu Result object: %w", err)
		}
		results = append(results, result)
	case '[':
		if err := json.Unmarshal(raw, &results); err != nil {
			return "", fmt.Errorf("unmarshal baidu Result array: %w", err)
		}
	default:
		return "", fmt.Errorf("unsupported baidu Result shape")
	}

	for _, result := range results {
		md := strings.TrimSpace(result.NewMarketData.MarketData)
		if md != "" {
			return md, nil
		}
	}
	return "", fmt.Errorf("empty marketData")
}

// parseFloatBaidu 解析百度数据中的数值字段，处理 "--" 等无效值
func parseFloatBaidu(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" || s == "--" {
		return 0
	}
	// 去除可能的 "+" 号前缀
	s = strings.TrimPrefix(s, "+")
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return v
}

// HealthCheck 数据源健康检查
func (b *BaiduDailyBarsSource) HealthCheck(ctx context.Context) error {
	checkCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	_, err := b.FetchDailyBars(checkCtx, "600000", "SH", InstrumentTypeStock)
	if err != nil {
		return fmt.Errorf("baidu daily bars source health check failed: %w", err)
	}
	return nil
}
