package stockv2

import (
	"context"
	"errors"
	"io"
	"math"
	"net/http"
	"strings"
	"testing"
	"time"
)

const thsDailyBarsFixture = `quotebridge_v6_line_hs_002457_00_last3({"data":"20260708,14.93,16.30,14.14,15.48,87073222,1313594070.00,26.127,,54800.00,848304;20260709,15.16,15.37,14.59,15.06,63297016,946834370.00,18.993,,90700.00,1365942;20260710,14.70,14.89,14.58,14.62,2463800,36179542.00,0.473,,,0"})`

func TestParseTHSDailyBarsMapsBaiduEquivalentFields(t *testing.T) {
	fetchedAt := time.Date(2026, 7, 10, 15, 5, 0, 0, time.UTC)
	bars, err := parseTHSDailyBars([]byte(thsDailyBarsFixture), "002457", "SZ", fetchedAt)
	if err != nil {
		t.Fatalf("parse 10jqka daily bars: %v", err)
	}
	if len(bars) != 3 {
		t.Fatalf("len(bars) = %d, want 3", len(bars))
	}
	got := bars[1]
	if got.Symbol != "002457" || got.Market != "SZ" || got.TradeDate != "2026-07-09" || got.Source != "10jqka_kline" {
		t.Fatalf("identity fields = %+v", got)
	}
	if got.Open != 15.16 || got.High != 15.37 || got.Low != 14.59 || got.Close != 15.06 {
		t.Fatalf("OHLC fields = %+v", got)
	}
	if got.Volume != 63297016 || got.Amount != 946834370 || got.TurnoverRate != 18.993 || got.PrevClose != 15.48 {
		t.Fatalf("market fields = %+v", got)
	}
	if !got.AmountPresent || !got.TurnoverRatePresent {
		t.Fatalf("present market fields were not marked present: %+v", got)
	}
	wantPct := (15.06 - 15.48) / 15.48 * 100
	if math.Abs(got.PctChange-wantPct) > 1e-9 {
		t.Fatalf("pctChange = %f, want %f", got.PctChange, wantPct)
	}
}

func TestParseTHSDailyBarsDoesNotTurnMissingFacetsIntoZero(t *testing.T) {
	body := []byte(`callback({"data":"20260710,10,11,9,10.5,100,--,,"})`)
	bars, err := parseTHSDailyBars(body, "600000", "SH", time.Now())
	if err != nil || len(bars) != 1 {
		t.Fatalf("bars=%+v err=%v", bars, err)
	}
	if bars[0].AmountPresent || bars[0].TurnoverRatePresent || bars[0].Quality != DailyBarQualityPartial {
		t.Fatalf("missing THS facets were claimed present: %+v", bars[0])
	}
}

func TestTHSDailyBarsRequestHelpers(t *testing.T) {
	if got := thsDailyBarsRequestSymbol("839008", "BJ"); got != "920008" {
		t.Fatalf("legacy BSE request symbol = %q, want 920008", got)
	}
	if got := thsDailyBarsRequestSymbol("920008", "BJ"); got != "920008" {
		t.Fatalf("current BSE request symbol = %q, want 920008", got)
	}
	got := thsDailyBarsRequestCount("2025-07-10", time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC))
	if got < 330 || got > 350 {
		t.Fatalf("one-year request count = %d, want a bounded one-request window", got)
	}
}

func TestTHSDailyBarsSourceStopsNetworkRequestsDuringAccessDeniedCooldown(t *testing.T) {
	requests := 0
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		return dailyBarHTTPResponse(req, http.StatusForbidden, ""), nil
	})}
	source := NewTHSDailyBarsSource(client)
	for i := 0; i < 2; i++ {
		_, err := source.FetchDailyBars(context.Background(), "002457", "SZ", "2026-07-01", "2026-07-10")
		if !errors.Is(err, errTHSDailyBarsAccessDenied) {
			t.Fatalf("call %d err = %v, want access denied", i+1, err)
		}
	}
	if requests != 1 {
		t.Fatalf("network requests = %d, want 1 during cooldown", requests)
	}
}

func TestFetchDailyBarsForMissingRangesUsesOneTHSRequest(t *testing.T) {
	var thsCalls, tencentCalls, baiduCalls int
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Host {
		case "d.10jqka.com.cn":
			thsCalls++
			return dailyBarHTTPResponse(req, http.StatusOK, thsDailyBarsFixture), nil
		case "web.ifzq.gtimg.cn":
			tencentCalls++
		case "finance.pae.baidu.com":
			baiduCalls++
		}
		return dailyBarHTTPResponse(req, http.StatusInternalServerError, ""), nil
	})}
	svc := NewService(nil, nil, client)
	bars, _, err := svc.fetchDailyBarsForMissingRanges(context.Background(), StockV2Instrument{
		Symbol: "002457", Market: "SZ", InstrumentType: InstrumentTypeStock,
	}, []dailyBarMissingRange{{Start: "2026-07-08", End: "2026-07-08"}, {Start: "2026-07-10", End: "2026-07-10"}})
	if err != nil {
		t.Fatalf("fetch missing ranges: %v", err)
	}
	if thsCalls != 1 || tencentCalls != 0 || baiduCalls != 0 {
		t.Fatalf("calls = 10jqka:%d Tencent:%d Baidu:%d", thsCalls, tencentCalls, baiduCalls)
	}
	if len(bars) != 2 || bars[0].TradeDate != "2026-07-08" || bars[1].TradeDate != "2026-07-10" {
		t.Fatalf("filtered bars = %+v", bars)
	}
}

func TestFetchDailyBarsForMissingRangesFallsBackInOrder(t *testing.T) {
	var thsCalls, tencentCalls, baiduCalls int
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Host {
		case "d.10jqka.com.cn":
			thsCalls++
			return dailyBarHTTPResponse(req, http.StatusBadGateway, ""), nil
		case "web.ifzq.gtimg.cn":
			tencentCalls++
			return dailyBarHTTPResponse(req, http.StatusBadGateway, ""), nil
		case "finance.pae.baidu.com":
			baiduCalls++
			return dailyBarHTTPResponse(req, http.StatusOK, `{"ResultCode":"0","Result":{"newMarketData":{"marketData":"1783612800,2026-07-10,14.70,14.62,2463800,14.89,14.58,36179542,0,0,0.473,15.06"}}}`), nil
		default:
			t.Fatalf("unexpected host %q", req.URL.Host)
		}
		return nil, nil
	})}
	svc := NewService(nil, nil, client)
	bars, _, err := svc.fetchDailyBarsForMissingRanges(context.Background(), StockV2Instrument{
		Symbol: "002457", Market: "SZ", InstrumentType: InstrumentTypeStock,
	}, []dailyBarMissingRange{{Start: "2026-07-10", End: "2026-07-10"}})
	if err != nil {
		t.Fatalf("fetch with fallbacks: %v", err)
	}
	if thsCalls != 1 || tencentCalls != 1 || baiduCalls != 1 {
		t.Fatalf("calls = 10jqka:%d Tencent:%d Baidu:%d", thsCalls, tencentCalls, baiduCalls)
	}
	if len(bars) != 1 || bars[0].Source != "baidu_kline" {
		t.Fatalf("fallback bars = %+v", bars)
	}
}

func TestFetchDailyBarsForMissingRangesKeepsTencentWhenBaiduCompletionFails(t *testing.T) {
	var thsCalls, tencentCalls, baiduCalls int
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Host {
		case "d.10jqka.com.cn":
			thsCalls++
			return dailyBarHTTPResponse(req, http.StatusBadGateway, ""), nil
		case "web.ifzq.gtimg.cn":
			tencentCalls++
			return dailyBarHTTPResponse(req, http.StatusOK, `{"code":0,"msg":"","data":{"sz002457":{"day":[["2026-07-10","14.70","14.62","14.89","14.58","24638"]]}}}`), nil
		case "finance.pae.baidu.com":
			baiduCalls++
		}
		return dailyBarHTTPResponse(req, http.StatusInternalServerError, ""), nil
	})}
	svc := NewService(nil, nil, client)
	bars, _, err := svc.fetchDailyBarsForMissingRanges(context.Background(), StockV2Instrument{
		Symbol: "002457", Market: "SZ", InstrumentType: InstrumentTypeStock,
	}, []dailyBarMissingRange{{Start: "2026-07-10", End: "2026-07-10"}})
	if err != nil {
		t.Fatalf("fetch with Tencent fallback: %v", err)
	}
	if thsCalls != 1 || tencentCalls != 1 || baiduCalls != 1 {
		t.Fatalf("calls = 10jqka:%d Tencent:%d Baidu:%d", thsCalls, tencentCalls, baiduCalls)
	}
	if len(bars) != 1 || bars[0].Source != "tencent_fqkline" || bars[0].Amount != 0 || bars[0].TurnoverRate != 0 {
		t.Fatalf("Tencent fallback bars = %+v", bars)
	}
}

func TestFetchDailyBarsForMissingRangesPreservesBaiduAccessDenied(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Host {
		case "d.10jqka.com.cn", "web.ifzq.gtimg.cn":
			return dailyBarHTTPResponse(req, http.StatusBadGateway, ""), nil
		case "finance.pae.baidu.com":
			return dailyBarHTTPResponse(req, http.StatusOK, `{"ResultCode":"403","Result":{}}`), nil
		}
		return nil, errors.New("unexpected host")
	})}
	svc := NewService(nil, nil, client)
	_, _, err := svc.fetchDailyBarsForMissingRanges(context.Background(), StockV2Instrument{
		Symbol: "002457", Market: "SZ", InstrumentType: InstrumentTypeStock,
	}, []dailyBarMissingRange{{Start: "2026-07-10", End: "2026-07-10"}})
	if !errors.Is(err, errBaiduDailyBarsAccessDenied) {
		t.Fatalf("err = %v, want Baidu access denied sentinel", err)
	}
}

func TestFetchDailyBarsForMissingRangesRequiresTwoSourcesForNegativeCoverage(t *testing.T) {
	tencentOutsideRange := `{"code":0,"msg":"","data":{"sh600000":{"day":[["2026-07-08","10","10","10","10","100"]]}}}`
	baiduOutsideRange := `{"ResultCode":"0","Result":{"newMarketData":{"marketData":"1783526400,2026-07-08,10,10,100,10,10,1000,0,0,1,10"}}}`
	t.Run("one empty success is not enough", func(t *testing.T) {
		client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			switch req.URL.Host {
			case "d.10jqka.com.cn", "finance.pae.baidu.com":
				return dailyBarHTTPResponse(req, http.StatusBadGateway, ""), nil
			case "web.ifzq.gtimg.cn":
				return dailyBarHTTPResponse(req, http.StatusOK, tencentOutsideRange), nil
			default:
				return nil, errors.New("unexpected host")
			}
		})}
		_, confirmed, err := NewService(nil, nil, client).fetchDailyBarsForMissingRanges(context.Background(), StockV2Instrument{
			Symbol: "600000", Market: "SH", InstrumentType: InstrumentTypeStock,
		}, []dailyBarMissingRange{{Start: "2026-07-10", End: "2026-07-10"}})
		if err == nil || confirmed {
			t.Fatalf("confirmed=%v err=%v, want unconfirmed error", confirmed, err)
		}
	})
	t.Run("two independent empty successes allow negative coverage", func(t *testing.T) {
		client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			switch req.URL.Host {
			case "d.10jqka.com.cn":
				return dailyBarHTTPResponse(req, http.StatusBadGateway, ""), nil
			case "web.ifzq.gtimg.cn":
				return dailyBarHTTPResponse(req, http.StatusOK, tencentOutsideRange), nil
			case "finance.pae.baidu.com":
				return dailyBarHTTPResponse(req, http.StatusOK, baiduOutsideRange), nil
			default:
				return nil, errors.New("unexpected host")
			}
		})}
		bars, confirmed, err := NewService(nil, nil, client).fetchDailyBarsForMissingRanges(context.Background(), StockV2Instrument{
			Symbol: "600000", Market: "SH", InstrumentType: InstrumentTypeStock,
		}, []dailyBarMissingRange{{Start: "2026-07-10", End: "2026-07-10"}})
		if err != nil || !confirmed || len(bars) != 0 {
			t.Fatalf("bars=%v confirmed=%v err=%v", bars, confirmed, err)
		}
	})
}

func dailyBarHTTPResponse(req *http.Request, status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}
}
