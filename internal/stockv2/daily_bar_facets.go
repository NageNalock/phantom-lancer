package stockv2

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	eastmoneyDailyFlowBackoffKey = "daily_bar:eastmoney_flow"
	eastmoneyDailyFlowBackoff    = 10 * time.Minute
)

func (s *Service) repairStoredDailyBarFlowFacets(ctx context.Context, inst StockV2Instrument, startDate, endDate string) (bool, error) {
	if s == nil || s.store == nil || normalizeInstrumentType(inst.InstrumentType) != InstrumentTypeStock {
		return false, nil
	}
	bars, err := s.store.GetDailyBars(ctx, inst.Symbol, DailyBarAdjustedNone, startDate, endDate, 0)
	if err != nil {
		return false, err
	}
	candidates := make([]StockV2DailyBar, 0)
	for _, bar := range bars {
		if dailyBarAmountPresent(bar) && dailyBarTurnoverPresent(bar) &&
			(!dailyBarNetInflowPresent(bar) || !dailyBarMainNetInflowPresent(bar)) {
			candidates = append(candidates, bar)
		}
	}
	if len(candidates) == 0 {
		return false, nil
	}
	statuses := s.enrichDailyBarsWithDataFacets(ctx, inst, candidates)
	for _, status := range statuses {
		if status.Status == "failed" {
			return true, errors.New(status.Message)
		}
	}
	if err := s.store.UpsertDailyBars(ctx, candidates); err != nil {
		return true, err
	}
	return true, nil
}

func (s *Service) enrichDailyBarsWithDataFacets(ctx context.Context, inst StockV2Instrument, bars []StockV2DailyBar) []AssetMaintenanceSourceStatus {
	if len(bars) == 0 || s == nil || normalizeInstrumentType(inst.InstrumentType) != InstrumentTypeStock {
		return nil
	}
	needsFlow := false
	for _, bar := range bars {
		if !bar.NetInflowPresent || !bar.MainNetInflowPresent {
			needsFlow = true
			break
		}
	}
	if !needsFlow {
		return nil
	}
	status := AssetMaintenanceSourceStatus{Source: "eastmoney_fflow_daykline", Status: "ok", CheckedAt: time.Now()}
	facets, err := s.fetchEastmoneyDailyFlowFacets(ctx, inst)
	if err != nil {
		status.Status = "failed"
		status.Message = err.Error()
		return []AssetMaintenanceSourceStatus{status}
	}
	if len(facets) == 0 {
		status.Status = "skipped"
		status.Message = "empty response"
		return []AssetMaintenanceSourceStatus{status}
	}
	for i := range bars {
		if facet, ok := facets[bars[i].TradeDate]; ok {
			if facet.NetInflowPresent {
				bars[i].NetInflow = facet.NetInflow
				bars[i].NetInflowPresent = true
			}
			if facet.MainNetInflowPresent {
				bars[i].MainNetInflow = facet.MainNetInflow
				bars[i].MainNetInflowPresent = true
			}
			bars[i].DataPayload = facet.Raw
		}
		if dailyBarFacetsComplete(bars[i]) {
			bars[i].Quality = DailyBarQualityOK
		} else {
			bars[i].Quality = DailyBarQualityPartial
		}
	}
	return []AssetMaintenanceSourceStatus{status}
}

type dailyBarDataFacet struct {
	NetInflow            float64
	MainNetInflow        float64
	NetInflowPresent     bool
	MainNetInflowPresent bool
	Raw                  string
}

func (s *Service) fetchEastmoneyDailyFlowFacets(ctx context.Context, inst StockV2Instrument) (map[string]dailyBarDataFacet, error) {
	if until, blocked := s.assetSourceBackoffUntil(eastmoneyDailyFlowBackoffKey, time.Now()); blocked {
		return nil, fmt.Errorf("eastmoney daily flow cooldown until %s", until.Format(time.RFC3339))
	}
	secid := eastmoneySecID(inst.Market, inst.Symbol)
	if secid == "" {
		return nil, fmt.Errorf("empty eastmoney secid")
	}
	values := url.Values{}
	values.Set("lmt", "500")
	values.Set("klt", "101")
	values.Set("secid", secid)
	values.Set("fields1", "f1,f2,f3,f7")
	values.Set("fields2", "f51,f52,f53,f54,f55,f56,f57,f58,f59,f60,f61,f62,f63")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://push2his.eastmoney.com/api/qt/stock/fflow/daykline/get?"+values.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Referer", "https://quote.eastmoney.com/")
	req.Header.Set("User-Agent", "Mozilla/5.0")
	client := s.httpClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		s.recordAssetSourceBackoff(eastmoneyDailyFlowBackoffKey, eastmoneyDailyFlowBackoff)
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		s.recordAssetSourceBackoff(eastmoneyDailyFlowBackoffKey, eastmoneyDailyFlowBackoff)
		return nil, fmt.Errorf("eastmoney fflow status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		s.recordAssetSourceBackoff(eastmoneyDailyFlowBackoffKey, eastmoneyDailyFlowBackoff)
		return nil, err
	}
	facets, err := parseEastmoneyDailyFlowFacets(body)
	if err != nil {
		s.recordAssetSourceBackoff(eastmoneyDailyFlowBackoffKey, eastmoneyDailyFlowBackoff)
		return nil, err
	}
	s.clearAssetSourceBackoff(eastmoneyDailyFlowBackoffKey)
	return facets, nil
}

func parseEastmoneyDailyFlowFacets(body []byte) (map[string]dailyBarDataFacet, error) {
	var resp struct {
		RC   int `json:"rc"`
		Data struct {
			KLines []string `json:"klines"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	if resp.RC != 0 {
		return nil, fmt.Errorf("eastmoney fflow rc %d", resp.RC)
	}
	out := make(map[string]dailyBarDataFacet, len(resp.Data.KLines))
	for _, line := range resp.Data.KLines {
		parts := strings.Split(line, ",")
		if len(parts) < 4 {
			continue
		}
		date := strings.TrimSpace(parts[0])
		if date == "" {
			continue
		}
		// 东财资金流字段: date, 主力净流入, 小单净流入, 中单净流入, 大单净流入, 超大单净流入, ...
		// MainNetInflow = 主力净流入(parts[1])
		// NetInflow = 全部净流入 = 主力 + 小单 + 中单
		mainNet, mainPresent := parseDailyBarFloatWithPresence(parts[1])
		smallNet, smallPresent := parseDailyBarFloatWithPresence(parts[2])
		midNet, midPresent := parseDailyBarFloatWithPresence(parts[3])
		netPresent := mainPresent && smallPresent && midPresent
		out[date] = dailyBarDataFacet{
			MainNetInflow:        mainNet,
			NetInflow:            mainNet + smallNet + midNet,
			NetInflowPresent:     netPresent,
			MainNetInflowPresent: mainPresent,
			Raw:                  line,
		}
	}
	return out, nil
}
