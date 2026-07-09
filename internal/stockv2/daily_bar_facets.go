package stockv2

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func (s *Service) enrichDailyBarsWithDataFacets(ctx context.Context, inst StockV2Instrument, bars []StockV2DailyBar) []AssetMaintenanceSourceStatus {
	if len(bars) == 0 || s == nil || s.httpClient == nil || normalizeInstrumentType(inst.InstrumentType) != InstrumentTypeStock {
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
			bars[i].NetInflow = facet.NetInflow
			bars[i].MainNetInflow = facet.MainNetInflow
			bars[i].DataPayload = facet.Raw
		}
	}
	return []AssetMaintenanceSourceStatus{status}
}

type dailyBarDataFacet struct {
	NetInflow     float64
	MainNetInflow float64
	Raw           string
}

func (s *Service) fetchEastmoneyDailyFlowFacets(ctx context.Context, inst StockV2Instrument) (map[string]dailyBarDataFacet, error) {
	secid := eastmoneySecID(inst.Market, inst.Symbol)
	if secid == "" {
		return nil, fmt.Errorf("empty eastmoney secid")
	}
	values := url.Values{}
	values.Set("lmt", "260")
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
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("eastmoney fflow status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		return nil, err
	}
	return parseEastmoneyDailyFlowFacets(body)
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
		if len(parts) < 3 {
			continue
		}
		date := strings.TrimSpace(parts[0])
		if date == "" {
			continue
		}
		out[date] = dailyBarDataFacet{
			MainNetInflow: parseFloatTencent(parts[1]),
			NetInflow:     parseFloatTencent(parts[1]),
			Raw:           line,
		}
	}
	return out, nil
}
