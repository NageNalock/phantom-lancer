package stockv2

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	thsDailyBarsMaxCount       = 1800
	thsDailyBarsCountBuffer    = 80
	thsDailyBarsBlockedFor     = 10 * time.Minute
	thsDailyBarsResponseMaxLen = 4 * 1024 * 1024
)

var errTHSDailyBarsAccessDenied = errors.New("10jqka daily bars access denied")

// THSDailyBarsSource reads unadjusted daily bars from Tonghuashun's public line endpoint.
// One response contains OHLCV, amount, and turnover rate, so replacing the Baidu
// full-history request does not require another request for those fields.
type THSDailyBarsSource struct {
	httpClient   *http.Client
	blockedMu    sync.Mutex
	blockedUntil time.Time
}

func NewTHSDailyBarsSource(client *http.Client) *THSDailyBarsSource {
	return &THSDailyBarsSource{httpClient: client}
}

func (s *THSDailyBarsSource) FetchDailyBars(ctx context.Context, symbol, market, startDate, endDate string) ([]StockV2DailyBar, error) {
	symbol, explicitMarket := normalizeQuoteSymbolInput(symbol)
	if !isSixDigitSymbol(symbol) {
		return nil, fmt.Errorf("invalid symbol for 10jqka daily bars: %s", symbol)
	}
	if market == "" {
		market = explicitMarket
	}
	_, market = marketPrefix(market, symbol)
	if err := s.checkBlocked(); err != nil {
		return nil, err
	}

	requestSymbol := thsDailyBarsRequestSymbol(symbol, market)
	count := thsDailyBarsRequestCount(startDate, time.Now())
	endpoint := fmt.Sprintf("https://d.10jqka.com.cn/v6/line/hs_%s/00/last%d.js", requestSymbol, count)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("build 10jqka daily bars request: %w", err)
	}
	req.Header.Set("User-Agent", pickDailyBarsUA())
	req.Header.Set("Referer", "https://stockpage.10jqka.com.cn/")

	client := s.httpClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("10jqka daily bars request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
		s.block()
		return nil, fmt.Errorf("%w: HTTP %d", errTHSDailyBarsAccessDenied, resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("10jqka daily bars HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, thsDailyBarsResponseMaxLen))
	if err != nil {
		return nil, fmt.Errorf("read 10jqka daily bars response: %w", err)
	}
	bars, err := parseTHSDailyBars(body, symbol, market, time.Now())
	if err != nil {
		return nil, err
	}
	if len(bars) == 0 {
		return nil, fmt.Errorf("10jqka returned 0 daily bars for %s", symbol)
	}

	filtered := bars[:0]
	for _, bar := range bars {
		if startDate != "" && bar.TradeDate < startDate {
			continue
		}
		if endDate != "" && bar.TradeDate > endDate {
			continue
		}
		filtered = append(filtered, bar)
	}
	return filtered, nil
}

func (s *THSDailyBarsSource) checkBlocked() error {
	s.blockedMu.Lock()
	defer s.blockedMu.Unlock()
	if time.Now().Before(s.blockedUntil) {
		return fmt.Errorf("%w: cooldown active", errTHSDailyBarsAccessDenied)
	}
	return nil
}

func (s *THSDailyBarsSource) block() {
	s.blockedMu.Lock()
	s.blockedUntil = time.Now().Add(thsDailyBarsBlockedFor)
	s.blockedMu.Unlock()
}

func thsDailyBarsRequestSymbol(symbol, market string) string {
	if strings.EqualFold(market, "BJ") && (strings.HasPrefix(symbol, "4") || strings.HasPrefix(symbol, "8")) {
		// ponytail: Tonghuashun serves legacy BSE 4/8 codes under their 920xxx code;
		// remove this mapping after the local instrument universe migrates to 92 codes.
		return "920" + symbol[3:]
	}
	return symbol
}

func thsDailyBarsRequestCount(startDate string, now time.Time) int {
	start, err := time.Parse("2006-01-02", startDate)
	if err != nil || start.After(now) {
		return thsDailyBarsCountBuffer + 1
	}
	days := int(now.Sub(start).Hours()/24) + 1
	count := (days*5+6)/7 + thsDailyBarsCountBuffer + 1 // include one prior bar for PrevClose
	if count > thsDailyBarsMaxCount {
		return thsDailyBarsMaxCount
	}
	return count
}

func parseTHSDailyBars(body []byte, symbol, market string, fetchedAt time.Time) ([]StockV2DailyBar, error) {
	body = bytes.TrimSpace(body)
	open := bytes.IndexByte(body, '(')
	close := bytes.LastIndexByte(body, ')')
	if open < 0 || close <= open {
		return nil, errors.New("decode 10jqka daily bars: invalid JSONP")
	}
	var payload struct {
		Data string `json:"data"`
	}
	if err := json.Unmarshal(body[open+1:close], &payload); err != nil {
		return nil, fmt.Errorf("decode 10jqka daily bars: %w", err)
	}
	if strings.TrimSpace(payload.Data) == "" {
		return nil, errors.New("decode 10jqka daily bars: empty data")
	}

	rows := strings.Split(payload.Data, ";")
	bars := make([]StockV2DailyBar, 0, len(rows))
	var prevClose float64
	for _, row := range rows {
		parts := strings.Split(strings.TrimSpace(row), ",")
		if len(parts) < 8 {
			continue
		}
		parsedDate, err := time.Parse("20060102", strings.TrimSpace(parts[0]))
		if err != nil {
			continue
		}
		openPrice := parseFloatTencent(parts[1])
		high := parseFloatTencent(parts[2])
		low := parseFloatTencent(parts[3])
		closePrice := parseFloatTencent(parts[4])
		if openPrice <= 0 || closePrice <= 0 {
			continue
		}
		if high < low {
			high, low = low, high
		}
		pctChange := 0.0
		if prevClose > 0 {
			pctChange = (closePrice - prevClose) / prevClose * 100
		}
		amount, amountPresent := parseDailyBarFloatWithPresence(parts[6])
		turnoverRate, turnoverRatePresent := parseDailyBarFloatWithPresence(parts[7])
		quality := DailyBarQualityOK
		if !amountPresent || !turnoverRatePresent {
			quality = DailyBarQualityPartial
		}
		bars = append(bars, StockV2DailyBar{
			ID:                  generateID(),
			Symbol:              symbol,
			Market:              market,
			TradeDate:           parsedDate.Format("2006-01-02"),
			Open:                openPrice,
			High:                high,
			Low:                 low,
			Close:               closePrice,
			PrevClose:           prevClose,
			Volume:              parseFloatTencent(parts[5]),
			Amount:              amount,
			PctChange:           pctChange,
			TurnoverRate:        turnoverRate,
			AmountPresent:       amountPresent,
			TurnoverRatePresent: turnoverRatePresent,
			Adjusted:            DailyBarAdjustedNone,
			Source:              "10jqka_kline",
			FetchedAt:           fetchedAt,
			Quality:             quality,
		})
		prevClose = closePrice
	}
	return bars, nil
}
