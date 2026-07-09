package stockv2

import "testing"

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
