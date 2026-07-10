package stockv2

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestParseBaiduMarketDataAcceptsResultObject(t *testing.T) {
	body := []byte(`{
		"ResultCode":"0",
		"Result":{
			"newMarketData":{
				"marketData":"1783555200,2026-07-09,15.16,15.06,63297016,15.37,14.59,946834366,-0.42,-2.713,18.99,15.48"
			}
		}
	}`)

	bars, err := parseBaiduMarketData(body, "002457", "SZ")
	if err != nil {
		t.Fatalf("parse baidu object result: %v", err)
	}
	assertParsedBaiduBar(t, bars)
}

func TestParseBaiduMarketDataAcceptsResultArray(t *testing.T) {
	body := []byte(`{
		"ResultCode":"0",
		"Result":[{
			"newMarketData":{
				"marketData":"1783555200,2026-07-09,15.16,15.06,63297016,15.37,14.59,946834366,-0.42,-2.713,18.99,15.48"
			}
		}]
	}`)

	bars, err := parseBaiduMarketData(body, "002457", "SZ")
	if err != nil {
		t.Fatalf("parse baidu array result: %v", err)
	}
	assertParsedBaiduBar(t, bars)
}

func TestParseBaiduMarketDataAcceptsNumericResultCode(t *testing.T) {
	body := []byte(`{
		"ResultCode": 0,
		"Result": {"newMarketData": {"marketData": "1783526400,2026-07-09,15.16,15.06,63297016,15.37,14.59,946834366,-0.42,-2.71,18.99,15.48"}}
	}`)
	bars, err := parseBaiduMarketData(body, "002457", "SZ")
	if err != nil {
		t.Fatalf("parse numeric Baidu ResultCode: %v", err)
	}
	assertParsedBaiduBar(t, bars)
}

func TestParseBaiduMarketDataReturnsAccessDeniedSentinel(t *testing.T) {
	_, err := parseBaiduMarketData([]byte(`{"ResultCode":"403","Result":{}}`), "002457", "SZ")
	if !errors.Is(err, errBaiduDailyBarsAccessDenied) {
		t.Fatalf("err = %v, want errBaiduDailyBarsAccessDenied", err)
	}
}

func TestParseBaiduMarketDataPreservesMissingFacetPresence(t *testing.T) {
	body := []byte(`{
		"ResultCode":"0",
		"Result":{"newMarketData":{"marketData":"1783555200,2026-07-09,15.16,15.06,63297016,15.37,14.59,--,-0.42,-2.713,,15.48"}}
	}`)
	bars, err := parseBaiduMarketData(body, "002457", "SZ")
	if err != nil || len(bars) != 1 {
		t.Fatalf("bars=%+v err=%v", bars, err)
	}
	if bars[0].AmountPresent || bars[0].TurnoverRatePresent || bars[0].Quality != DailyBarQualityPartial {
		t.Fatalf("missing Baidu facets were claimed present: %+v", bars[0])
	}
}

func TestBaiduDailyBarsSourceStopsNetworkRequestsDuringAccessDeniedCooldown(t *testing.T) {
	requests := 0
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"ResultCode":"403","Result":{}}`)),
			Request:    req,
		}, nil
	})}
	source := NewBaiduDailyBarsSource(client)
	source.minInterval = time.Millisecond
	for i := 0; i < 2; i++ {
		_, err := source.FetchDailyBars(context.Background(), "002457", "SZ", InstrumentTypeStock)
		if !errors.Is(err, errBaiduDailyBarsAccessDenied) {
			t.Fatalf("call %d err = %v, want access denied", i+1, err)
		}
	}
	if requests != 1 {
		t.Fatalf("network requests = %d, want 1 during cooldown", requests)
	}
}

func TestBaiduDailyBarsSourceThrottlesRequests(t *testing.T) {
	var mu sync.Mutex
	var startedAt []time.Time
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		mu.Lock()
		startedAt = append(startedAt, time.Now())
		mu.Unlock()
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(`{
				"ResultCode":"0",
				"Result":{
					"newMarketData":{
						"marketData":"1783555200,2026-07-09,15.16,15.06,63297016,15.37,14.59,946834366,-0.42,-2.713,18.99,15.48"
					}
				}
			}`)),
		}, nil
	})}
	source := NewBaiduDailyBarsSource(client)
	source.minInterval = 25 * time.Millisecond

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := source.FetchDailyBars(context.Background(), "002457", "SZ", InstrumentTypeStock)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("FetchDailyBars error: %v", err)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if len(startedAt) != 2 {
		t.Fatalf("request count = %d, want 2", len(startedAt))
	}
	sort.Slice(startedAt, func(i, j int) bool { return startedAt[i].Before(startedAt[j]) })
	if elapsed := startedAt[1].Sub(startedAt[0]); elapsed < 20*time.Millisecond {
		t.Fatalf("requests were not throttled: elapsed=%s", elapsed)
	}
}

func assertParsedBaiduBar(t *testing.T, bars []StockV2DailyBar) {
	t.Helper()
	if len(bars) != 1 {
		t.Fatalf("len(bars) = %d, want 1", len(bars))
	}
	bar := bars[0]
	if bar.TradeDate != "2026-07-09" || bar.Source != "baidu_kline" || bar.Adjusted != DailyBarAdjustedNone {
		t.Fatalf("unexpected bar identity: %+v", bar)
	}
	if bar.Open != 15.16 || bar.Close != 15.06 || bar.High != 15.37 || bar.Low != 14.59 {
		t.Fatalf("unexpected OHLC: %+v", bar)
	}
	if bar.Volume != 63297016 || bar.Amount != 946834366 || bar.TurnoverRate != 18.99 {
		t.Fatalf("unexpected volume/amount/turnover: %+v", bar)
	}
}
