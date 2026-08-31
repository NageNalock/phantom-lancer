package stockv2

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"phantom-lancer/internal/safelog"

	"golang.org/x/text/encoding/simplifiedchinese"
)

const (
	maxLatestQuoteSymbols      = 200
	intradayQuotePruneInterval = 6 * time.Hour
)

var chinaMarketTZ = time.FixedZone("Asia/Shanghai", 8*60*60)

type quoteSymbol struct {
	Input       string
	Symbol      string
	Market      string
	TencentCode string
	EastmoneyID string
}

func (s *Service) RefreshLatestQuotes(ctx context.Context, symbols []string, triggerSource string) (QuoteRefreshResult, error) {
	if triggerSource == "" {
		triggerSource = "system"
	}
	specs, failures, err := normalizeQuoteSymbols(symbols)
	if err != nil {
		return QuoteRefreshResult{}, err
	}

	result := QuoteRefreshResult{
		FailedItems: append([]UpdateFailure{}, failures...),
		FetchedAt:   time.Now(),
	}
	for _, failure := range failures {
		s.recordQuoteRefreshFailure(ctx, failure.Symbol, "", failure.Reason, result.FetchedAt)
	}
	if len(specs) == 0 {
		result.FailedCount = len(result.FailedItems)
		return result, nil
	}

	// ponytail: batch latest quote is only supplemental/fallback; real 1m bars own the intraday timeline.
	supplementalQuotes, supplementalFailures := s.fetchLatestQuotesForSpecsWithFailures(ctx, specs)
	supplementalBySymbol := make(map[string]StockV2QuoteLatest, len(supplementalQuotes))
	for _, quote := range supplementalQuotes {
		supplementalBySymbol[quote.Symbol] = quote
	}
	supplementalFailureBySymbol := make(map[string]string, len(supplementalFailures))
	for _, failure := range supplementalFailures {
		supplementalFailureBySymbol[failure.Symbol] = failure.Reason
	}

	var fetchFailures []UpdateFailure
	for _, spec := range specs {
		bars, name, minuteErr := s.fetchRecentMinuteBars(ctx, spec, result.FetchedAt)
		minuteWarning := ""
		if minuteErr != nil {
			minuteWarning = "minute sync degraded: " + minuteErr.Error()
		} else if len(bars) == 0 {
			minuteWarning = "minute sync degraded: minute kline source returned 0 bars"
		}
		var quote StockV2QuoteLatest
		hasQuote := false
		insertFallbackSnapshot := false
		if minuteErr == nil && len(bars) > 0 {
			if err := s.store.UpsertMinuteBars(ctx, bars); err != nil {
				fetchFailures = append(fetchFailures, UpdateFailure{
					Symbol: spec.Symbol,
					Reason: safelog.Error(err, 240),
				})
				continue
			}
			supplemental, ok := supplementalBySymbol[spec.Symbol]
			quote = s.projectLatestQuoteFromMinuteBars(ctx, spec, bars, name, result.FetchedAt, supplemental, ok)
			hasQuote = true
		} else if fallback, ok := supplementalBySymbol[spec.Symbol]; ok {
			quote = fallback
			hasQuote = true
			insertFallbackSnapshot = true
		}
		if !hasQuote {
			reason := "no minute kline returned from public source"
			if minuteErr != nil {
				reason = minuteErr.Error()
			}
			if fallbackReason := supplementalFailureBySymbol[spec.Symbol]; fallbackReason != "" {
				reason += "; latest quote fallback: " + fallbackReason
			}
			fetchFailures = append(fetchFailures, UpdateFailure{Symbol: spec.Symbol, Reason: reason})
			continue
		}
		if err := s.store.UpsertLatestQuote(ctx, quote); err != nil {
			fetchFailures = append(fetchFailures, UpdateFailure{
				Symbol: spec.Symbol,
				Reason: safelog.Error(err, 240),
			})
			continue
		}
		if insertFallbackSnapshot {
			if err := s.store.InsertQuoteSnapshot(ctx, StockV2QuoteSnapshot{StockV2QuoteLatest: quote, CollectedAt: result.FetchedAt}); err != nil {
				fetchFailures = append(fetchFailures, UpdateFailure{
					Symbol: spec.Symbol,
					Reason: safelog.Error(err, 240),
				})
				continue
			}
		}
		if insertFallbackSnapshot && minuteWarning != "" && s.log != nil && shouldLogQuoteRefreshWarning(triggerSource) {
			s.log.Warn("stockv2 minute quote sync degraded",
				"symbol", quote.Symbol,
				"source", quote.Source,
				"trigger_source", triggerSource,
				"warning", safelog.Text(minuteWarning, 240),
			)
		}
		s.recordQuoteRefreshSuccess(ctx, quote, minuteWarning)
		result.Items = append(result.Items, quote)
		result.RefreshedCount++
	}
	if result.RefreshedCount > 0 {
		s.pruneIntradayQuotesIfDue(ctx, result.FetchedAt)
	}

	specBySymbol := make(map[string]quoteSymbol, len(specs))
	for _, spec := range specs {
		specBySymbol[spec.Symbol] = spec
	}
	for _, failure := range fetchFailures {
		failure.Reason = safelog.Text(failure.Reason, 240)
		result.FailedItems = append(result.FailedItems, failure)
		spec := specBySymbol[failure.Symbol]
		s.recordQuoteRefreshFailure(ctx, failure.Symbol, spec.Market, failure.Reason, result.FetchedAt)
		if oldQuote, ok, err := s.store.MarkLatestQuoteFailed(ctx, failure.Symbol, failure.Reason); err != nil {
			return result, err
		} else if ok {
			result.Items = append(result.Items, oldQuote)
		}
	}

	result.FailedCount = len(result.FailedItems)
	if result.FailedCount > 0 && s.log != nil && shouldLogQuoteRefreshWarning(triggerSource) {
		s.log.Warn("stockv2 latest quote refresh completed with failures", "trigger_source", triggerSource, "requested_count", len(symbols), "refreshed_count", result.RefreshedCount, "failed_count", result.FailedCount, "failure_sample", stockV2FailureSample(result.FailedItems, 5))
	}
	return result, nil
}

func (s *Service) pruneIntradayQuotesIfDue(ctx context.Context, now time.Time) {
	if s == nil || s.store == nil {
		return
	}
	if now.IsZero() {
		now = time.Now()
	}
	s.quotePruneMu.Lock()
	if !s.lastQuotePrune.IsZero() && now.Sub(s.lastQuotePrune) < intradayQuotePruneInterval {
		s.quotePruneMu.Unlock()
		return
	}
	s.lastQuotePrune = now
	s.quotePruneMu.Unlock()
	if err := s.store.PruneIntradayQuotes(ctx, now.AddDate(0, 0, -5)); err != nil && s.log != nil {
		s.log.Warn("stockv2 prune intraday quotes failed", "error", safelog.Text(err.Error(), 240))
	}
}

func (s *Service) fetchRecentMinuteBars(ctx context.Context, spec quoteSymbol, fetchedAt time.Time) ([]StockV2MinuteBar, string, error) {
	since := fetchedAt.In(chinaMarketTZ).AddDate(0, 0, -5)
	if latest, ok, err := s.store.GetLatestMinuteBar(ctx, spec.Symbol); err != nil {
		return nil, "", err
	} else if ok && isAStockRegularTradingMinute(latest.MinuteAt) {
		overlapSince := latest.MinuteAt.In(chinaMarketTZ).Add(-30 * time.Minute)
		if overlapSince.After(since) {
			since = overlapSince
		}
	}
	tencentBars, tencentName, tencentErr := s.fetchTencentMinuteBars(ctx, spec, since, fetchedAt.In(chinaMarketTZ))
	if tencentErr == nil && len(tencentBars) > 0 {
		return tencentBars, tencentName, nil
	}
	if tencentErr == nil {
		tencentErr = errors.New("tencent minute returned 0 bars")
	}
	bars, name, err := s.fetchEastmoneyMinuteBars(ctx, spec, since, fetchedAt.In(chinaMarketTZ))
	if err == nil && len(bars) > 0 {
		return bars, name, nil
	}
	if err == nil {
		err = errors.New("eastmoney minute returned 0 bars")
	}
	if err != nil && tencentErr != nil {
		return nil, "", fmt.Errorf("tencent minute failed: %v; eastmoney minute failed: %w", tencentErr, err)
	}
	if tencentErr != nil {
		return nil, "", tencentErr
	}
	if err != nil {
		return nil, "", err
	}
	return nil, "", errors.New("minute kline source returned 0 bars")
}

func (s *Service) fetchEastmoneyMinuteBars(ctx context.Context, spec quoteSymbol, since, until time.Time) ([]StockV2MinuteBar, string, error) {
	if spec.EastmoneyID == "" {
		return nil, "", errors.New("empty eastmoney secid")
	}
	if until.IsZero() {
		until = time.Now().In(chinaMarketTZ)
	}
	if since.IsZero() {
		since = until.AddDate(0, 0, -5)
	}
	values := url.Values{}
	values.Set("secid", spec.EastmoneyID)
	values.Set("klt", "1")
	values.Set("fqt", "0")
	values.Set("beg", since.In(chinaMarketTZ).Format("20060102"))
	values.Set("end", until.In(chinaMarketTZ).Format("20060102"))
	values.Set("lmt", "1500")
	values.Set("fields1", "f1,f2,f3,f4,f5,f6")
	values.Set("fields2", "f51,f52,f53,f54,f55,f56,f57,f58,f59,f60,f61")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://push2his.eastmoney.com/api/qt/stock/kline/get?"+values.Encode(), nil)
	if err != nil {
		return nil, "", fmt.Errorf("create eastmoney minute kline request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/149.0.0.0 Safari/537.36")
	req.Header.Set("Referer", "https://quote.eastmoney.com/")
	client := s.httpClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("eastmoney minute kline request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("eastmoney minute kline http status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024))
	if err != nil {
		return nil, "", fmt.Errorf("read eastmoney minute kline response: %w", err)
	}
	bars, name, err := parseEastmoneyMinuteKLineResponse(body, spec)
	if err != nil {
		return nil, "", err
	}
	filtered := bars[:0]
	for _, bar := range bars {
		if bar.MinuteAt.Before(since) || bar.MinuteAt.After(until.Add(time.Minute)) {
			continue
		}
		filtered = append(filtered, bar)
	}
	return filtered, name, nil
}

func (s *Service) fetchTencentMinuteBars(ctx context.Context, spec quoteSymbol, since, until time.Time) ([]StockV2MinuteBar, string, error) {
	prefix, market := marketPrefix(spec.Market, spec.Symbol)
	code := prefix + spec.Symbol
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://web.ifzq.gtimg.cn/appstock/app/minute/query?code="+url.QueryEscape(code), nil)
	if err != nil {
		return nil, "", fmt.Errorf("create tencent minute request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36")
	client := s.httpClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("tencent minute request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("tencent minute http status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024))
	if err != nil {
		return nil, "", fmt.Errorf("read tencent minute response: %w", err)
	}
	bars, name, err := parseTencentMinuteQueryResponse(body, quoteSymbol{Symbol: spec.Symbol, Market: market, TencentCode: code})
	if err != nil {
		return nil, "", err
	}
	filtered := bars[:0]
	for _, bar := range bars {
		if bar.MinuteAt.Before(since) || bar.MinuteAt.After(until.Add(time.Minute)) {
			continue
		}
		filtered = append(filtered, bar)
	}
	return filtered, name, nil
}

func parseEastmoneyMinuteKLineResponse(body []byte, spec quoteSymbol) ([]StockV2MinuteBar, string, error) {
	var resp struct {
		RC   int `json:"rc"`
		Data *struct {
			Code   string   `json:"code"`
			Market int      `json:"market"`
			Name   string   `json:"name"`
			KLines []string `json:"klines"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, "", fmt.Errorf("decode eastmoney minute kline response: %w", err)
	}
	if resp.RC != 0 {
		return nil, "", fmt.Errorf("eastmoney minute kline rc %d", resp.RC)
	}
	if resp.Data == nil {
		return nil, "", errors.New("eastmoney minute kline missing data")
	}
	name := strings.TrimSpace(resp.Data.Name)
	bars := make([]StockV2MinuteBar, 0, len(resp.Data.KLines))
	for _, line := range resp.Data.KLines {
		bar, ok := parseEastmoneyMinuteKLineRow(line, spec)
		if ok {
			bars = append(bars, bar)
		}
	}
	sort.SliceStable(bars, func(i, j int) bool { return bars[i].MinuteAt.Before(bars[j].MinuteAt) })
	return bars, name, nil
}

func parseEastmoneyMinuteKLineRow(line string, spec quoteSymbol) (StockV2MinuteBar, bool) {
	fields := strings.Split(strings.TrimSpace(line), ",")
	if len(fields) < 7 {
		return StockV2MinuteBar{}, false
	}
	minuteAt, err := time.ParseInLocation("2006-01-02 15:04", strings.TrimSpace(fields[0]), chinaMarketTZ)
	if err != nil || !isAStockRegularTradingMinute(minuteAt) {
		return StockV2MinuteBar{}, false
	}
	open := parseFloatTencent(fields[1])
	closePrice := parseFloatTencent(fields[2])
	high := parseFloatTencent(fields[3])
	low := parseFloatTencent(fields[4])
	if open <= 0 || closePrice <= 0 || high <= 0 || low <= 0 {
		return StockV2MinuteBar{}, false
	}
	pctChange := 0.0
	if len(fields) > 8 {
		pctChange = parseFloatTencent(fields[8])
	}
	prevClose := 0.0
	if pctChange != -100 && pctChange != 0 {
		prevClose = closePrice / (1 + pctChange/100)
	}
	return StockV2MinuteBar{
		Symbol:        spec.Symbol,
		Market:        spec.Market,
		MinuteAt:      minuteAt,
		Open:          open,
		High:          high,
		Low:           low,
		Close:         closePrice,
		PrevClose:     prevClose,
		Volume:        parseFloatTencent(fields[5]),
		Amount:        parseFloatTencent(fields[6]),
		PctChange:     pctChange,
		SnapshotCount: 1,
		Source:        QuoteSourceEastmoneyMinute,
	}, true
}

func parseTencentMinuteQueryResponse(body []byte, spec quoteSymbol) ([]StockV2MinuteBar, string, error) {
	var resp struct {
		Code int `json:"code"`
		Data map[string]struct {
			Data struct {
				Rows []string `json:"data"`
				Date string   `json:"date"`
			} `json:"data"`
			QT map[string][]string `json:"qt"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, "", fmt.Errorf("decode tencent minute response: %w", err)
	}
	if resp.Code != 0 {
		return nil, "", fmt.Errorf("tencent minute code %d", resp.Code)
	}
	code := strings.ToLower(strings.TrimSpace(spec.TencentCode))
	item, ok := resp.Data[code]
	if !ok {
		for key, candidate := range resp.Data {
			if strings.HasSuffix(strings.ToLower(key), spec.Symbol) {
				item, ok = candidate, true
				code = strings.ToLower(key)
				break
			}
		}
	}
	if !ok {
		return nil, "", fmt.Errorf("tencent minute data missing symbol %s", spec.Symbol)
	}
	name := ""
	prevClose := 0.0
	if qt := item.QT[code]; len(qt) > 1 {
		name = strings.TrimSpace(qt[1])
		if len(qt) > 4 {
			prevClose = parseFloatTencent(qt[4])
		}
	}
	bars := make([]StockV2MinuteBar, 0, len(item.Data.Rows))
	var prevPrice, prevVolume, prevAmount float64
	for _, row := range item.Data.Rows {
		bar, ok := parseTencentMinuteRow(row, item.Data.Date, spec, prevClose, prevPrice, prevVolume, prevAmount)
		if !ok {
			continue
		}
		prevPrice = bar.Close
		prevVolume += bar.Volume
		prevAmount += bar.Amount
		bars = append(bars, bar)
	}
	sort.SliceStable(bars, func(i, j int) bool { return bars[i].MinuteAt.Before(bars[j].MinuteAt) })
	return bars, name, nil
}

func parseTencentMinuteRow(row, tradeDate string, spec quoteSymbol, prevClose, prevPrice, prevVolume, prevAmount float64) (StockV2MinuteBar, bool) {
	fields := strings.Fields(strings.TrimSpace(row))
	if len(fields) < 4 || len(tradeDate) != 8 {
		return StockV2MinuteBar{}, false
	}
	minuteAt, err := time.ParseInLocation("200601021504", tradeDate+fields[0], chinaMarketTZ)
	if err != nil || !isAStockRegularTradingMinute(minuteAt) {
		return StockV2MinuteBar{}, false
	}
	price := parseFloatTencent(fields[1])
	cumulativeVolume := parseFloatTencent(fields[2])
	cumulativeAmount := parseFloatTencent(fields[3])
	if price <= 0 {
		return StockV2MinuteBar{}, false
	}
	open := prevPrice
	if open <= 0 {
		open = price
	}
	high, low := open, price
	if price > high {
		high = price
	}
	if price < low {
		low = price
	}
	volume, amount := cumulativeVolume, cumulativeAmount
	if prevVolume > 0 {
		volume = nonNegativeDelta(prevVolume, cumulativeVolume)
	}
	if prevAmount > 0 {
		amount = nonNegativeDelta(prevAmount, cumulativeAmount)
	}
	pctChange := 0.0
	if prevClose > 0 {
		pctChange = (price - prevClose) / prevClose * 100
	}
	return StockV2MinuteBar{
		Symbol:        spec.Symbol,
		Market:        spec.Market,
		MinuteAt:      minuteAt,
		Open:          open,
		High:          high,
		Low:           low,
		Close:         price,
		PrevClose:     prevClose,
		Volume:        volume,
		Amount:        amount,
		PctChange:     pctChange,
		SnapshotCount: 1,
		Source:        QuoteSourceTencentMinute,
	}, true
}

func (s *Service) projectLatestQuoteFromMinuteBars(ctx context.Context, spec quoteSymbol, bars []StockV2MinuteBar, name string, fetchedAt time.Time, supplemental StockV2QuoteLatest, hasSupplemental bool) StockV2QuoteLatest {
	sort.SliceStable(bars, func(i, j int) bool { return bars[i].MinuteAt.Before(bars[j].MinuteAt) })
	last := bars[len(bars)-1]
	localDate := last.MinuteAt.In(chinaMarketTZ).Format("2006-01-02")
	open, high, low, volume, amount := 0.0, 0.0, 0.0, 0.0, 0.0
	for _, bar := range bars {
		if bar.MinuteAt.In(chinaMarketTZ).Format("2006-01-02") != localDate {
			continue
		}
		if open == 0 {
			open = bar.Open
		}
		if bar.High > high {
			high = bar.High
		}
		if low == 0 || bar.Low < low {
			low = bar.Low
		}
		volume += bar.Volume
		amount += bar.Amount
	}
	if name == "" && hasSupplemental {
		name = supplemental.Name
	}
	if name == "" {
		if instrument, err := s.store.GetInstrument(ctx, spec.Symbol); err == nil {
			name = instrument.Name
		}
	}
	prevClose := last.PrevClose
	if prevClose <= 0 && hasSupplemental {
		prevClose = supplemental.PrevClose
	}
	pctChange := last.PctChange
	if pctChange == 0 && prevClose > 0 {
		pctChange = (last.Close - prevClose) / prevClose * 100
	}
	quote := StockV2QuoteLatest{
		Symbol:    spec.Symbol,
		Market:    spec.Market,
		Name:      name,
		LastPrice: last.Close,
		PrevClose: prevClose,
		OpenPrice: open,
		HighPrice: high,
		LowPrice:  low,
		Volume:    volume,
		Amount:    amount,
		PctChange: pctChange,
		QuoteAt:   last.MinuteAt,
		FetchedAt: fetchedAt,
		Source:    firstNonEmpty(last.Source, QuoteSourceEastmoneyMinute),
		Status:    QuoteStatusFresh,
	}
	if hasSupplemental {
		quote.Amplitude = supplemental.Amplitude
		quote.TurnoverRate = supplemental.TurnoverRate
		quote.VolumeRatio = supplemental.VolumeRatio
		quote.MainNetInflow = supplemental.MainNetInflow
		quote.SuperNetInflow = supplemental.SuperNetInflow
		quote.LargeNetInflow = supplemental.LargeNetInflow
		quote.MediumNetInflow = supplemental.MediumNetInflow
		quote.SmallNetInflow = supplemental.SmallNetInflow
		quote.MainNetInflowPct = supplemental.MainNetInflowPct
		if quote.OpenPrice == 0 {
			quote.OpenPrice = supplemental.OpenPrice
		}
		if quote.HighPrice == 0 {
			quote.HighPrice = supplemental.HighPrice
		}
		if quote.LowPrice == 0 {
			quote.LowPrice = supplemental.LowPrice
		}
		if quote.Volume == 0 {
			quote.Volume = supplemental.Volume
		}
		if quote.Amount == 0 {
			quote.Amount = supplemental.Amount
		}
	}
	return quote
}

func (s *Service) recordQuoteRefreshSuccess(ctx context.Context, quote StockV2QuoteLatest, warnings ...string) {
	warning := ""
	if len(warnings) > 0 {
		warning = safelog.Text(warnings[0], 240)
	}
	// ponytail: status rows are best-effort observability; quote persistence above is the source of truth.
	_ = s.store.UpsertQuoteRefreshStatus(ctx, QuoteRefreshStatus{
		Symbol:        quote.Symbol,
		Market:        quote.Market,
		Source:        quote.Source,
		Status:        QuoteStatusFresh,
		LastAttemptAt: quote.FetchedAt,
		LastSuccessAt: quote.FetchedAt,
		ErrorMessage:  warning,
	})
}

func shouldLogQuoteRefreshWarning(triggerSource string) bool {
	switch strings.ToLower(strings.TrimSpace(triggerSource)) {
	case "", "system", "monitor", "scheduled":
		return false
	default:
		return true
	}
}

func (s *Service) recordQuoteRefreshFailure(ctx context.Context, symbol, market, reason string, attemptedAt time.Time) {
	if attemptedAt.IsZero() {
		attemptedAt = time.Now()
	}
	// ponytail: keep only latest failure state; add append-only audit only if a real debugging need appears.
	_ = s.store.UpsertQuoteRefreshStatus(ctx, QuoteRefreshStatus{
		Symbol:        strings.TrimSpace(symbol),
		Market:        market,
		Source:        QuoteSourceEastmoneyMinute,
		Status:        QuoteStatusFailed,
		LastAttemptAt: attemptedAt,
		LastFailureAt: attemptedAt,
		ErrorMessage:  reason,
	})
}

func (s *Service) GetLatestQuotes(ctx context.Context, symbols []string) ([]StockV2QuoteLatest, error) {
	specs, failures, err := normalizeQuoteSymbols(symbols)
	if err != nil {
		return nil, err
	}
	if len(failures) > 0 {
		return nil, fmt.Errorf("%w: %s", ErrInvalidQuoteSymbol, failures[0].Symbol)
	}
	normalized := make([]string, 0, len(specs))
	for _, spec := range specs {
		normalized = append(normalized, spec.Symbol)
	}
	return s.store.GetLatestQuotes(ctx, normalized)
}

func (s *Service) GetLatestQuoteRefreshState(ctx context.Context, limit int) (QuoteRefreshTaskState, []QuoteRefreshStatus, error) {
	state, err := s.store.GetQuoteRefreshTaskState(ctx, MonitorTaskLatestQuoteRefresh)
	if err != nil {
		return QuoteRefreshTaskState{}, nil, err
	}
	if state == nil {
		state = &QuoteRefreshTaskState{TaskType: MonitorTaskLatestQuoteRefresh, Status: "idle"}
	}
	items, err := s.store.ListQuoteRefreshStatuses(ctx, limit)
	if err != nil {
		return QuoteRefreshTaskState{}, nil, err
	}
	return *state, items, nil
}

func (s *Service) ListMinuteBars(ctx context.Context, symbol string, days int, limit int) ([]StockV2MinuteBar, error) {
	symbol = strings.TrimSpace(symbol)
	if !isSixDigitSymbol(symbol) {
		return nil, ErrInvalidQuoteSymbol
	}
	if days <= 0 || days > 5 {
		days = 5
	}
	if limit <= 0 {
		limit = days * 240
	}
	return s.store.ListMinuteBars(ctx, symbol, time.Now().AddDate(0, 0, -days), limit)
}

func (s *Service) fetchLatestQuotesForSpecsWithFailures(ctx context.Context, specs []quoteSymbol) ([]StockV2QuoteLatest, []UpdateFailure) {
	const batchSize = 80

	var quotes []StockV2QuoteLatest
	var failures []UpdateFailure
	for start := 0; start < len(specs); start += batchSize {
		end := start + batchSize
		if end > len(specs) {
			end = len(specs)
		}

		batch := specs[start:end]
		batchQuotes, err := s.fetchEastmoneyLatestQuoteBatch(ctx, batch)
		if err != nil || len(batchQuotes) == 0 {
			batchQuotes, err = s.fetchTencentLatestQuoteBatch(ctx, batch)
			if err != nil {
				reason := safelog.Error(err, 240)
				for _, spec := range batch {
					failures = append(failures, UpdateFailure{Symbol: spec.Symbol, Reason: reason})
				}
				continue
			}
			quotes = append(quotes, batchQuotes...)
			continue
		}

		seen := make(map[string]struct{}, len(batchQuotes))
		for _, quote := range batchQuotes {
			seen[quote.Symbol] = struct{}{}
		}
		var missing []quoteSymbol
		for _, spec := range batch {
			if _, ok := seen[spec.Symbol]; !ok {
				missing = append(missing, spec)
			}
		}
		quotes = append(quotes, batchQuotes...)
		if len(missing) > 0 {
			fallback, err := s.fetchTencentLatestQuoteBatch(ctx, missing)
			if err != nil {
				reason := safelog.Error(err, 240)
				for _, spec := range missing {
					failures = append(failures, UpdateFailure{Symbol: spec.Symbol, Reason: reason})
				}
			} else {
				quotes = append(quotes, fallback...)
			}
		}
	}
	return quotes, failures
}

func (s *Service) fetchEastmoneyLatestQuoteBatch(ctx context.Context, specs []quoteSymbol) ([]StockV2QuoteLatest, error) {
	if len(specs) == 0 {
		return nil, nil
	}
	secids := make([]string, 0, len(specs))
	specBySecID := make(map[string]quoteSymbol, len(specs))
	for _, spec := range specs {
		if spec.EastmoneyID == "" {
			continue
		}
		secids = append(secids, spec.EastmoneyID)
		specBySecID[spec.EastmoneyID] = spec
	}
	if len(secids) == 0 {
		return nil, errors.New("empty eastmoney secids")
	}
	values := url.Values{}
	values.Set("secids", strings.Join(secids, ","))
	values.Set("fields", "f2,f3,f4,f5,f6,f7,f8,f10,f12,f13,f14,f15,f16,f17,f18,f62,f66,f69,f72,f75,f78,f81,f84,f87,f124,f184")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://push2his.eastmoney.com/api/qt/ulist.np/get?"+values.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("create eastmoney quote request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/149.0.0.0 Safari/537.36")
	req.Header.Set("Referer", "https://quote.eastmoney.com/")
	client := s.httpClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("eastmoney quote request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("eastmoney quote http status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("read eastmoney quote response: %w", err)
	}
	return parseEastmoneyLatestQuoteResponse(body, specBySecID, time.Now())
}

func (s *Service) fetchTencentLatestQuoteBatch(ctx context.Context, specs []quoteSymbol) ([]StockV2QuoteLatest, error) {
	if len(specs) == 0 {
		return nil, nil
	}

	codes := make([]string, 0, len(specs))
	specByCode := make(map[string]quoteSymbol, len(specs))
	for _, spec := range specs {
		codes = append(codes, spec.TencentCode)
		specByCode[spec.TencentCode] = spec
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://qt.gtimg.cn/q="+strings.Join(codes, ","), nil)
	if err != nil {
		return nil, fmt.Errorf("create tencent quote request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")

	client := s.httpClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tencent quote request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("tencent quote http status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("read tencent quote response: %w", err)
	}
	return parseTencentLatestQuoteResponse(body, specByCode, time.Now())
}

func normalizeQuoteSymbols(symbols []string) ([]quoteSymbol, []UpdateFailure, error) {
	if len(symbols) == 0 {
		return nil, nil, ErrQuoteSymbolsRequired
	}
	if len(symbols) > maxLatestQuoteSymbols {
		return nil, nil, ErrTooManyQuoteSymbols
	}

	seen := make(map[string]struct{}, len(symbols))
	specs := make([]quoteSymbol, 0, len(symbols))
	var failures []UpdateFailure
	for _, raw := range symbols {
		input := strings.TrimSpace(raw)
		symbol, explicitMarket := normalizeQuoteSymbolInput(input)
		if !isSixDigitSymbol(symbol) {
			failures = append(failures, UpdateFailure{Symbol: input, Reason: "invalid quote symbol"})
			continue
		}
		market := explicitMarket
		if market == "" {
			market = inferAStockMarket(symbol)
		}
		if market == "" {
			failures = append(failures, UpdateFailure{Symbol: symbol, Reason: "unknown quote market"})
			continue
		}
		if _, ok := seen[market+":"+symbol]; ok {
			continue
		}
		seen[market+":"+symbol] = struct{}{}
		specs = append(specs, quoteSymbol{
			Input:       input,
			Symbol:      symbol,
			Market:      market,
			TencentCode: strings.ToLower(market) + symbol,
			EastmoneyID: eastmoneySecID(market, symbol),
		})
	}
	return specs, failures, nil
}

func eastmoneySecID(market, symbol string) string {
	switch strings.ToUpper(strings.TrimSpace(market)) {
	case "SH":
		return "1." + symbol
	case "SZ", "BJ":
		return "0." + symbol
	default:
		return ""
	}
}

func normalizeQuoteSymbolInput(input string) (string, string) {
	text := strings.ToUpper(strings.TrimSpace(input))
	text = strings.ReplaceAll(text, ".", "")
	text = strings.ReplaceAll(text, "-", "")
	text = strings.ReplaceAll(text, "_", "")
	text = strings.ReplaceAll(text, ":", "")
	for _, market := range []string{"SH", "SZ", "BJ"} {
		if strings.HasPrefix(text, market) {
			return strings.TrimPrefix(text, market), market
		}
	}
	return text, ""
}

func isSixDigitSymbol(symbol string) bool {
	if len(symbol) != 6 {
		return false
	}
	for _, r := range symbol {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func inferAStockMarket(symbol string) string {
	market, _ := inferInstrumentMarketAndType(symbol)
	return market
}

func inferInstrumentMarketAndType(symbol string) (string, string) {
	symbol = strings.TrimSpace(symbol)
	switch {
	case strings.HasPrefix(symbol, "5"):
		return "SH", InstrumentTypeExchangeFund
	case strings.HasPrefix(symbol, "15"), strings.HasPrefix(symbol, "16"), strings.HasPrefix(symbol, "18"):
		return "SZ", InstrumentTypeExchangeFund
	case strings.HasPrefix(symbol, "6"):
		return "SH", InstrumentTypeStock
	case strings.HasPrefix(symbol, "0"), strings.HasPrefix(symbol, "3"):
		return "SZ", InstrumentTypeStock
	case strings.HasPrefix(symbol, "8"), strings.HasPrefix(symbol, "4"):
		return "BJ", InstrumentTypeStock
	default:
		return "", ""
	}
}

func parseEastmoneyLatestQuoteResponse(body []byte, specBySecID map[string]quoteSymbol, fetchedAt time.Time) ([]StockV2QuoteLatest, error) {
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.UseNumber()
	var resp struct {
		RC   int `json:"rc"`
		Data struct {
			Diff []map[string]any `json:"diff"`
		} `json:"data"`
	}
	if err := decoder.Decode(&resp); err != nil {
		return nil, fmt.Errorf("decode eastmoney quote response: %w", err)
	}
	if resp.RC != 0 {
		return nil, fmt.Errorf("eastmoney quote rc %d", resp.RC)
	}
	quotes := make([]StockV2QuoteLatest, 0, len(resp.Data.Diff))
	for _, item := range resp.Data.Diff {
		quote, err := parseEastmoneyQuoteItem(item, specBySecID, fetchedAt)
		if err != nil || quote.Symbol == "" {
			continue
		}
		quotes = append(quotes, quote)
	}
	return quotes, nil
}

func parseEastmoneyQuoteItem(item map[string]any, specBySecID map[string]quoteSymbol, fetchedAt time.Time) (StockV2QuoteLatest, error) {
	symbol := strings.TrimSpace(eastmoneyString(item["f12"]))
	if symbol == "" {
		return StockV2QuoteLatest{}, errors.New("empty eastmoney quote symbol")
	}
	marketCode := int(eastmoneyFloat(item["f13"]))
	secid := strconv.Itoa(marketCode) + "." + symbol
	spec, ok := specBySecID[secid]
	if !ok {
		spec = quoteSymbol{Symbol: symbol, Market: eastmoneyMarket(marketCode)}
	}
	scale := eastmoneyPriceScale(symbol)
	lastPrice := eastmoneyFloat(item["f2"]) / scale
	prevClose := eastmoneyFloat(item["f18"]) / scale
	if lastPrice <= 0 || prevClose <= 0 {
		return StockV2QuoteLatest{}, errors.New("empty eastmoney quote price")
	}
	quoteAt := fetchedAt
	if ts := int64(eastmoneyFloat(item["f124"])); ts > 0 {
		quoteAt = time.Unix(ts, 0).In(chinaMarketTZ)
	}
	return StockV2QuoteLatest{
		Symbol:           symbol,
		Market:           spec.Market,
		Name:             strings.TrimSpace(eastmoneyString(item["f14"])),
		LastPrice:        lastPrice,
		PrevClose:        prevClose,
		OpenPrice:        eastmoneyFloat(item["f17"]) / scale,
		HighPrice:        eastmoneyFloat(item["f15"]) / scale,
		LowPrice:         eastmoneyFloat(item["f16"]) / scale,
		Volume:           eastmoneyFloat(item["f5"]),
		Amount:           eastmoneyFloat(item["f6"]),
		PctChange:        eastmoneyFloat(item["f3"]) / 100,
		Amplitude:        eastmoneyFloat(item["f7"]) / 100,
		TurnoverRate:     eastmoneyFloat(item["f8"]) / 100,
		VolumeRatio:      eastmoneyFloat(item["f10"]) / 100,
		MainNetInflow:    eastmoneyFloat(item["f62"]),
		SuperNetInflow:   eastmoneyFloat(item["f66"]),
		LargeNetInflow:   eastmoneyFloat(item["f72"]),
		MediumNetInflow:  eastmoneyFloat(item["f78"]),
		SmallNetInflow:   eastmoneyFloat(item["f84"]),
		MainNetInflowPct: eastmoneyFloat(item["f184"]) / 100,
		QuoteAt:          quoteAt,
		FetchedAt:        fetchedAt,
		Source:           QuoteSourceEastmoney,
		Status:           QuoteStatusFresh,
	}, nil
}

func eastmoneyFloat(value any) float64 {
	switch v := value.(type) {
	case json.Number:
		f, _ := v.Float64()
		return f
	case float64:
		return v
	case string:
		f, _ := strconv.ParseFloat(strings.TrimSpace(v), 64)
		return f
	default:
		return 0
	}
}

func eastmoneyString(value any) string {
	if s, ok := value.(string); ok {
		return s
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func eastmoneyMarket(code int) string {
	if code == 1 {
		return "SH"
	}
	return "SZ"
}

func eastmoneyPriceScale(symbol string) float64 {
	_, typ := inferInstrumentMarketAndType(symbol)
	if typ == InstrumentTypeExchangeFund {
		return 1000
	}
	return 100
}

func parseTencentLatestQuoteResponse(body []byte, specByCode map[string]quoteSymbol, fetchedAt time.Time) ([]StockV2QuoteLatest, error) {
	textBody := body
	if !utf8.Valid(body) {
		utf8Body, err := simplifiedchinese.GBK.NewDecoder().Bytes(body)
		if err != nil {
			return nil, fmt.Errorf("decode tencent quote response: %w", err)
		}
		textBody = utf8Body
	}
	var quotes []StockV2QuoteLatest
	scanner := bufio.NewScanner(strings.NewReader(string(textBody)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		quote, err := parseTencentLatestQuoteLine(line, specByCode, fetchedAt)
		if err != nil || quote.Symbol == "" {
			continue
		}
		quotes = append(quotes, quote)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan tencent quote response: %w", err)
	}
	return quotes, nil
}

func parseTencentLatestQuoteLine(line string, specByCode map[string]quoteSymbol, fetchedAt time.Time) (StockV2QuoteLatest, error) {
	line = strings.TrimSuffix(line, ";")
	eq := strings.IndexByte(line, '=')
	if eq < 0 {
		return StockV2QuoteLatest{}, errors.New("bad tencent quote line")
	}

	tencentCode := strings.TrimPrefix(strings.TrimSpace(line[:eq]), "v_")
	payload := strings.Trim(strings.TrimSpace(line[eq+1:]), `"`)
	if payload == "" {
		return StockV2QuoteLatest{}, errors.New("empty tencent quote payload")
	}

	fields := strings.Split(payload, "~")
	if len(fields) < 6 {
		return StockV2QuoteLatest{}, errors.New("short tencent quote payload")
	}

	spec, ok := specByCode[tencentCode]
	symbol := strings.TrimSpace(fields[2])
	if symbol == "" {
		return StockV2QuoteLatest{}, errors.New("empty quote symbol")
	}
	if !ok {
		spec = quoteSymbol{Symbol: symbol, Market: inferAStockMarket(symbol), TencentCode: tencentCode}
	}

	lastPrice := parseFloatTencent(fieldAt(fields, 3))
	prevClose := parseFloatTencent(fieldAt(fields, 4))
	if lastPrice <= 0 || prevClose <= 0 {
		return StockV2QuoteLatest{}, errors.New("empty quote price")
	}

	pctChange := parseFloatTencent(fieldAt(fields, 32))
	if pctChange == 0 && prevClose > 0 {
		pctChange = (lastPrice - prevClose) / prevClose * 100
	}

	quoteAt := parseTencentQuoteTime(fieldAt(fields, 30), fetchedAt)
	return StockV2QuoteLatest{
		Symbol:    symbol,
		Market:    spec.Market,
		Name:      strings.TrimSpace(fieldAt(fields, 1)),
		LastPrice: lastPrice,
		PrevClose: prevClose,
		OpenPrice: parseFloatTencent(fieldAt(fields, 5)),
		HighPrice: parseFloatTencent(fieldAt(fields, 33)),
		LowPrice:  parseFloatTencent(fieldAt(fields, 34)),
		Volume:    parseTencentQuoteVolume(fields),
		Amount:    parseFloatTencent(fieldAt(fields, 37)),
		PctChange: pctChange,
		QuoteAt:   quoteAt,
		FetchedAt: fetchedAt,
		Source:    QuoteSourceTencent,
		Status:    QuoteStatusFresh,
	}, nil
}

func fieldAt(fields []string, index int) string {
	if index < 0 || index >= len(fields) {
		return ""
	}
	return fields[index]
}

func parseTencentQuoteTime(value string, fallback time.Time) time.Time {
	value = strings.TrimSpace(value)
	if len(value) < len("20060102150405") {
		return fallback
	}
	parsed, err := time.ParseInLocation("20060102150405", value[:14], chinaMarketTZ)
	if err != nil {
		return fallback
	}
	return parsed
}

func parseTencentQuoteVolume(fields []string) float64 {
	if volume := parseFloatTencent(fieldAt(fields, 36)); volume > 0 {
		return volume
	}
	return parseFloatTencent(fieldAt(fields, 6))
}
