package stockv2

import (
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

	"phantom-lancer/internal/safelog"
)

const (
	// ponytail: fixed provider endpoints keep the owner-facing settings limited
	// to credentials and prevent an arbitrary URL from becoming an SSRF surface.
	opportunityFundFlowPrimaryURL     = "http://datahubco.com/app-api/openapi/v1/tushare/moneyflow"
	opportunityFundFlowBackupURL      = "https://ai-tool.indevs.in/tushare/pro/moneyflow"
	opportunityFundFlowSourcePrimary  = "datahubco"
	opportunityFundFlowSourceBackup   = "indevs"
	opportunityFundFlowRequestTimeout = 35 * time.Second
	opportunityFundFlowStageTimeout   = 5 * time.Minute
)

type opportunityFundFlowPoint struct {
	TradeDate string
	MainNet   float64
	Turnover  float64
}

type opportunityFundFlowFetchResult struct {
	Points []opportunityFundFlowPoint
	Source string
}

type opportunityFundFlowSource struct {
	Name, Endpoint, Key string
	Client              *http.Client
}

type tushareRelayPayload struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		Fields []string `json:"fields"`
		Items  [][]any  `json:"items"`
	} `json:"data"`
}

func (s *Service) fetchOpportunityMarketFundFlow(ctx context.Context, config OpportunityMarketScanConfig, symbol, market, endDate string, limit int) (opportunityFundFlowFetchResult, error) {
	tsCode := strings.TrimSpace(symbol)
	if strings.EqualFold(market, "SH") {
		tsCode += ".SH"
	} else {
		tsCode += ".SZ"
	}
	params := url.Values{"ts_code": {tsCode}, "end_date": {strings.ReplaceAll(endDate, "-", "")}, "limit": {strconv.Itoa(limit)}}
	sources := make([]opportunityFundFlowSource, 0, 2)
	if config.PrimaryFundFlowAPIKey != "" {
		sources = append(sources, opportunityFundFlowSource{opportunityFundFlowSourcePrimary, opportunityFundFlowPrimaryURL, config.PrimaryFundFlowAPIKey, opportunityFundFlowHTTPClient(nil)})
	}
	if config.BackupFundFlowAPIKey != "" {
		client, err := opportunityFundFlowBackupClient(config.BackupFundFlowProxy)
		if err != nil {
			return opportunityFundFlowFetchResult{}, err
		}
		sources = append(sources, opportunityFundFlowSource{opportunityFundFlowSourceBackup, opportunityFundFlowBackupURL, config.BackupFundFlowAPIKey, client})
	}
	if len(sources) == 0 {
		return opportunityFundFlowFetchResult{}, errors.New("fund flow credentials are not configured")
	}
	return fetchOpportunityFundFlowSources(ctx, sources, params)
}

func fetchOpportunityFundFlowSources(ctx context.Context, sources []opportunityFundFlowSource, params url.Values) (opportunityFundFlowFetchResult, error) {
	var summaries []string
	for _, source := range sources {
		for attempt := 0; attempt < 2; attempt++ {
			points, err := requestTushareMoneyFlow(ctx, source.Client, source.Endpoint, source.Key, params)
			if err == nil {
				return opportunityFundFlowFetchResult{Points: points, Source: source.Name}, nil
			}
			summaries = append(summaries, source.Name+": "+safelog.Error(err, 120))
			if attempt == 0 {
				select {
				case <-ctx.Done():
					return opportunityFundFlowFetchResult{}, ctx.Err()
				case <-time.After(800 * time.Millisecond):
				}
			}
		}
	}
	return opportunityFundFlowFetchResult{}, errors.New(strings.Join(summaries, "; "))
}

func opportunityFundFlowHTTPClient(proxyURL *url.URL) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	if proxyURL != nil {
		transport.Proxy = http.ProxyURL(proxyURL)
	}
	return &http.Client{Transport: transport, Timeout: opportunityFundFlowRequestTimeout}
}

func opportunityFundFlowBackupClient(rawProxy string) (*http.Client, error) {
	rawProxy = strings.TrimSpace(rawProxy)
	if rawProxy == "" {
		return opportunityFundFlowHTTPClient(nil), nil
	}
	parsed, err := url.Parse(rawProxy)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, errors.New("backup fund flow proxy must be an http or https URL")
	}
	return opportunityFundFlowHTTPClient(parsed), nil
}

func requestTushareMoneyFlow(ctx context.Context, client *http.Client, endpoint, apiKey string, params url.Values) ([]opportunityFundFlowPoint, error) {
	requestCtx, cancel := context.WithTimeout(ctx, opportunityFundFlowRequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, endpoint+"?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "phantom-lancer-stockv2/1.0")
	req.Header.Set("X-API-Key", apiKey)
	resp, err := client.Do(req)
	if err != nil {
		// ponytail: provider and proxy errors may echo credential-bearing URLs. Keep the
		// persisted diagnostic categorical; detailed transport errors stay out of logs.
		return nil, errors.New("network request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("provider returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, err
	}
	var payload tushareRelayPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, errors.New("provider returned invalid JSON")
	}
	if payload.Code != 0 {
		return nil, fmt.Errorf("provider business code %d: %s", payload.Code, safelog.Text(payload.Msg, 100))
	}
	return parseTushareMoneyFlow(payload.Data.Fields, payload.Data.Items)
}

func parseTushareMoneyFlow(fields []string, items [][]any) ([]opportunityFundFlowPoint, error) {
	index := make(map[string]int, len(fields))
	for i, field := range fields {
		index[field] = i
	}
	required := []string{"trade_date", "net_mf_amount", "buy_sm_amount", "sell_sm_amount", "buy_md_amount", "sell_md_amount", "buy_lg_amount", "sell_lg_amount", "buy_elg_amount", "sell_elg_amount"}
	for _, field := range required {
		if _, ok := index[field]; !ok {
			return nil, fmt.Errorf("moneyflow response missing field %s", field)
		}
	}
	out := make([]opportunityFundFlowPoint, 0, len(items))
	for _, item := range items {
		value := func(field string) float64 {
			i := index[field]
			if i >= len(item) {
				return 0
			}
			return floatFromAny(item[i])
		}
		if index["trade_date"] >= len(item) {
			continue
		}
		tradeDate := strings.TrimSpace(fmt.Sprint(item[index["trade_date"]]))
		if len(tradeDate) != 8 {
			continue
		}
		turnover := 0.0
		for _, field := range required[2:] {
			turnover += mathAbs(value(field)) * 10000
		}
		out = append(out, opportunityFundFlowPoint{TradeDate: tradeDate[:4] + "-" + tradeDate[4:6] + "-" + tradeDate[6:], MainNet: value("net_mf_amount") * 10000, Turnover: turnover})
	}
	if len(out) == 0 {
		return nil, errors.New("moneyflow response has no valid rows")
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TradeDate > out[j].TradeDate })
	return out, nil
}

func floatFromAny(value any) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case json.Number:
		result, _ := typed.Float64()
		return result
	case string:
		result, _ := strconv.ParseFloat(typed, 64)
		return result
	default:
		return 0
	}
}

func mathAbs(value float64) float64 {
	if value < 0 {
		return -value
	}
	return value
}

func applyOpportunityFundFlow(metrics *OpportunityMarketScanMetrics, points []opportunityFundFlowPoint, source string) {
	metrics.FundFlowAvailable = len(points) > 0
	metrics.FundFlowSource = source
	metrics.FundFlowStatus = "available"
	var net20, turnover20 float64
	for i, point := range points {
		distance := i + 1
		if i == 0 {
			metrics.FundFlowAsOf = point.TradeDate
		}
		if distance <= 5 {
			metrics.MainNetInflow5 += point.MainNet
		}
		if distance <= 20 {
			metrics.MainNetInflow20 += point.MainNet
			net20 += point.MainNet
			turnover20 += point.Turnover
			if point.MainNet > 0 {
				metrics.PositiveFlowDays20++
			}
		}
		if distance <= 60 {
			metrics.MainNetInflow60 += point.MainNet
		}
	}
	if turnover20 > 0 {
		metrics.MainFlowRatio20 = net20 / turnover20 * 100
	}
}

func opportunityFundFlowFailureStatus(err error) string {
	message := strings.ToLower(err.Error())
	for _, marker := range []string{"invalid json", "missing field", "no valid rows"} {
		if strings.Contains(message, marker) {
			return "invalid_data"
		}
	}
	return "source_unavailable"
}
