package stockv2

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	// ponytail: the configured relay returns an empty page for limit=6000 even
	// though the upstream daily API documents that ceiling. Two normal trading-day
	// pages at 5000 cover the current A-share universe; the third page is a bounded
	// guard for future growth and must be replaced by cursor pagination if exceeded.
	universeDailyBarPageSize = 5000
	universeDailyBarMaxPages = 3
)

func (s *Service) fetchUniverseDailyBars(ctx context.Context, config OpportunityMarketScanConfig, tradeDate string) ([]StockV2DailyBar, error) {
	tradeDate = strings.ReplaceAll(strings.TrimSpace(tradeDate), "-", "")
	if len(tradeDate) != 8 {
		return nil, errors.New("universe daily bars require an eight-digit trade date")
	}

	fetchedAt := time.Now()
	barsBySymbol := make(map[string]StockV2DailyBar, 6000)
	for page := 0; page < universeDailyBarMaxPages; page++ {
		result, err := s.fetchDecisionTushareDataset(ctx, config, "daily", url.Values{
			"trade_date": {tradeDate},
			"limit":      {strconv.Itoa(universeDailyBarPageSize)},
			"offset":     {strconv.Itoa(page * universeDailyBarPageSize)},
		})
		if err != nil {
			return nil, fmt.Errorf("fetch daily page %d: %w", page+1, err)
		}
		pageBars, err := parseTushareDailyBars(result, fetchedAt)
		if err != nil {
			return nil, fmt.Errorf("parse daily page %d: %w", page+1, err)
		}
		for _, bar := range pageBars {
			barsBySymbol[bar.Symbol] = bar
		}
		if len(result.Items) < universeDailyBarPageSize {
			break
		}
		if page == universeDailyBarMaxPages-1 {
			return nil, errors.New("daily pagination exceeded the bounded page limit")
		}
	}
	if len(barsBySymbol) == 0 {
		return nil, errors.New("daily response has no valid rows")
	}

	bars := make([]StockV2DailyBar, 0, len(barsBySymbol))
	for _, bar := range barsBySymbol {
		bars = append(bars, bar)
	}
	sort.Slice(bars, func(i, j int) bool { return bars[i].Symbol < bars[j].Symbol })
	return bars, nil
}

func parseTushareDailyBars(result tushareDatasetResult, fetchedAt time.Time) ([]StockV2DailyBar, error) {
	index := decisionFieldIndex(result.Fields)
	for _, field := range []string{"ts_code", "trade_date", "open", "high", "low", "close", "pre_close", "pct_chg", "vol", "amount"} {
		if _, ok := index[field]; !ok {
			return nil, fmt.Errorf("daily response missing field %s", field)
		}
	}

	out := make([]StockV2DailyBar, 0, len(result.Items))
	for _, row := range result.Items {
		tsCode := strings.ToUpper(decisionStringValue(row, index, "ts_code"))
		parts := strings.Split(tsCode, ".")
		if len(parts) != 2 || !isSixDigitSymbol(parts[0]) {
			continue
		}
		market := parts[1]
		if market != "SH" && market != "SZ" && market != "BJ" {
			continue
		}
		tradeDate := decisionDateValue(row, index, "trade_date")
		open := decisionNumberValue(row, index, "open")
		high := decisionNumberValue(row, index, "high")
		low := decisionNumberValue(row, index, "low")
		closePrice := decisionNumberValue(row, index, "close")
		if tradeDate == "" || open <= 0 || closePrice <= 0 || high < max(open, closePrice) || low <= 0 || low > min(open, closePrice) {
			continue
		}
		out = append(out, StockV2DailyBar{
			ID: generateID(), Symbol: parts[0], Market: market, TradeDate: tradeDate,
			Open: open, High: high, Low: low, Close: closePrice,
			PrevClose: decisionNumberValue(row, index, "pre_close"),
			Volume:    decisionNumberValue(row, index, "vol"),
			// Tushare daily amount is expressed in thousands of CNY. The local
			// market-data contract stores CNY so it can be compared with quotes.
			Amount:    decisionNumberValue(row, index, "amount") * 1000,
			PctChange: decisionNumberValue(row, index, "pct_chg"),
			Adjusted:  DailyBarAdjustedNone,
			Source:    "tushare_daily_" + result.Source,
			FetchedAt: fetchedAt,
			Quality:   DailyBarQualityOK,
		})
	}
	return out, nil
}
