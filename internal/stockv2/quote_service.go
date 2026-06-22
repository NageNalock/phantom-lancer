package stockv2

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"phantom-lancer/internal/safelog"

	"golang.org/x/text/encoding/simplifiedchinese"
)

const maxLatestQuoteSymbols = 200

var chinaMarketTZ = time.FixedZone("Asia/Shanghai", 8*60*60)

type quoteSymbol struct {
	Input       string
	Symbol      string
	Market      string
	TencentCode string
}

func (s *Service) FetchLatestQuotes(ctx context.Context, symbols []string) ([]StockV2QuoteLatest, error) {
	specs, failures, err := normalizeQuoteSymbols(symbols)
	if err != nil {
		return nil, err
	}
	if len(failures) > 0 {
		return nil, fmt.Errorf("%w: %s", ErrInvalidQuoteSymbol, failures[0].Symbol)
	}
	return s.fetchLatestQuotesForSpecs(ctx, specs)
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

	quotes, fetchFailures := s.fetchLatestQuotesForSpecsWithFailures(ctx, specs)
	quotesBySymbol := make(map[string]StockV2QuoteLatest, len(quotes))
	for _, quote := range quotes {
		quotesBySymbol[quote.Symbol] = quote
	}
	failedBySymbol := make(map[string]struct{}, len(fetchFailures))
	for _, failure := range fetchFailures {
		failedBySymbol[failure.Symbol] = struct{}{}
	}

	for _, spec := range specs {
		quote, ok := quotesBySymbol[spec.Symbol]
		if !ok {
			if _, alreadyFailed := failedBySymbol[spec.Symbol]; alreadyFailed {
				continue
			}
			fetchFailures = append(fetchFailures, UpdateFailure{
				Symbol: spec.Symbol,
				Reason: "no quote returned from tencent",
			})
			continue
		}
		if err := s.store.UpsertLatestQuote(ctx, quote); err != nil {
			fetchFailures = append(fetchFailures, UpdateFailure{
				Symbol: spec.Symbol,
				Reason: safelog.Error(err, 240),
			})
			continue
		}
		s.recordQuoteRefreshSuccess(ctx, quote)
		result.Items = append(result.Items, quote)
		result.RefreshedCount++
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
	return result, nil
}

func (s *Service) recordQuoteRefreshSuccess(ctx context.Context, quote StockV2QuoteLatest) {
	// ponytail: status rows are best-effort observability; quote persistence above is the source of truth.
	_ = s.store.UpsertQuoteRefreshStatus(ctx, QuoteRefreshStatus{
		Symbol:        quote.Symbol,
		Market:        quote.Market,
		Source:        quote.Source,
		Status:        QuoteStatusFresh,
		LastAttemptAt: quote.FetchedAt,
		LastSuccessAt: quote.FetchedAt,
	})
}

func (s *Service) recordQuoteRefreshFailure(ctx context.Context, symbol, market, reason string, attemptedAt time.Time) {
	if attemptedAt.IsZero() {
		attemptedAt = time.Now()
	}
	// ponytail: keep only latest failure state; add append-only audit only if a real debugging need appears.
	_ = s.store.UpsertQuoteRefreshStatus(ctx, QuoteRefreshStatus{
		Symbol:        strings.TrimSpace(symbol),
		Market:        market,
		Source:        QuoteSourceTencent,
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

func (s *Service) fetchLatestQuotesForSpecs(ctx context.Context, specs []quoteSymbol) ([]StockV2QuoteLatest, error) {
	quotes, failures := s.fetchLatestQuotesForSpecsWithFailures(ctx, specs)
	if len(failures) > 0 && len(quotes) == 0 {
		return nil, errors.New(failures[0].Reason)
	}
	return quotes, nil
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

		batchQuotes, err := s.fetchTencentLatestQuoteBatch(ctx, specs[start:end])
		if err != nil {
			reason := safelog.Error(err, 240)
			for _, spec := range specs[start:end] {
				failures = append(failures, UpdateFailure{Symbol: spec.Symbol, Reason: reason})
			}
			continue
		}
		quotes = append(quotes, batchQuotes...)
	}
	return quotes, failures
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
		})
	}
	return specs, failures, nil
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
