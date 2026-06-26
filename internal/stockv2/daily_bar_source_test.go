package stockv2

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestDailyBarsSourceFallsBackToFundNAVForExchangeFund(t *testing.T) {
	var sawFundNAV bool
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Host {
		case "web.ifzq.gtimg.cn":
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"code":0,"msg":"","data":{"sz160636":{"day":[]}}}`)),
				Request:    req,
			}, nil
		case "api.fund.eastmoney.com":
			sawFundNAV = true
			if got := req.URL.Query().Get("fundCode"); got != "160636" {
				t.Fatalf("fundCode = %q, want 160636", got)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body: io.NopCloser(strings.NewReader(`{
					"Data":{"LSJZList":[
						{"FSRQ":"2026-06-25","DWJZ":"1.5098","JZZZL":"3.50"},
						{"FSRQ":"2026-06-24","DWJZ":"1.4588","JZZZL":"2.23"}
					]},
					"ErrCode":0
				}`)),
				Request: req,
			}, nil
		default:
			t.Fatalf("unexpected host %q", req.URL.Host)
		}
		return nil, nil
	})}

	source := NewDailyBarsSource(nil, client)
	bars, err := source.FetchDailyBars(context.Background(), "160636", "SZ", "2026-06-24", "2026-06-25", DailyBarAdjustedNone, 20)
	if err != nil {
		t.Fatalf("fetch daily bars: %v", err)
	}
	if !sawFundNAV {
		t.Fatal("fund NAV fallback was not called")
	}
	if len(bars) != 2 {
		t.Fatalf("len(bars) = %d, want 2", len(bars))
	}
	if bars[0].TradeDate != "2026-06-24" || bars[1].TradeDate != "2026-06-25" {
		t.Fatalf("bars not sorted ascending: %+v", bars)
	}
	latest := bars[1]
	if latest.Source != "eastmoney_fund_nav" || latest.Quality != DailyBarQualityPartial {
		t.Fatalf("latest source/quality = %s/%s", latest.Source, latest.Quality)
	}
	if latest.Open != latest.Close || latest.High != latest.Close || latest.Low != latest.Close || latest.Volume != 0 || latest.Amount != 0 {
		t.Fatalf("fund NAV bar should preserve NAV as flat OHLC without volume/amount: %+v", latest)
	}
	if latest.PrevClose != bars[0].Close || latest.PctChange != 3.50 {
		t.Fatalf("latest prev/pct = %.4f/%.2f, want %.4f/3.50", latest.PrevClose, latest.PctChange, bars[0].Close)
	}
}
