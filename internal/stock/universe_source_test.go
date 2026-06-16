package stock

import (
	"encoding/json"
	"testing"
)

func TestFixSinaPseudoJSONHandlesUniverseVariants(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantCode   string
		wantSymbol string
		wantName   string
	}{
		{
			name:       "var assignment",
			input:      `var hq=[{symbol:"sh600000",code:"600000",name:"浦发银行",trade:"1"}];`,
			wantCode:   "600000",
			wantSymbol: "sh600000",
			wantName:   "浦发银行",
		},
		{
			name:       "jsonp callback with indexed name",
			input:      `IO.XSRV2.CallbackList['abc']([{symbol:"sz000001",code:"000001",name:"平安银行",trade:"1"}]);`,
			wantCode:   "000001",
			wantSymbol: "sz000001",
			wantName:   "平安银行",
		},
		{
			name:       "trailing script after array",
			input:      `[{symbol:"bj430047",code:"430047",name:"诺思兰德"}];sinaSSOController.feedBackUrlCallBack();`,
			wantCode:   "430047",
			wantSymbol: "bj430047",
			wantName:   "诺思兰德",
		},
		{
			name:       "single quoted strings",
			input:      `var hq=[{symbol:'sh600519',code:'600519',name:'贵州茅台'}];`,
			wantCode:   "600519",
			wantSymbol: "sh600519",
			wantName:   "贵州茅台",
		},
		{
			name:       "already valid json",
			input:      `[{"symbol":"sh601398","code":"601398","name":"工商银行"}]`,
			wantCode:   "601398",
			wantSymbol: "sh601398",
			wantName:   "工商银行",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixed := fixSinaPseudoJSON([]byte(tt.input))
			var rows []struct {
				Code   string `json:"code"`
				Symbol string `json:"symbol"`
				Name   string `json:"name"`
			}
			if err := json.Unmarshal(fixed, &rows); err != nil {
				t.Fatalf("unmarshal fixed response: %v; fixed=%s", err, fixed)
			}
			if len(rows) != 1 {
				t.Fatalf("rows = %d, want 1; fixed=%s", len(rows), fixed)
			}
			if rows[0].Code != tt.wantCode || rows[0].Symbol != tt.wantSymbol || rows[0].Name != tt.wantName {
				t.Fatalf("row = %+v, want code=%s symbol=%s name=%s", rows[0], tt.wantCode, tt.wantSymbol, tt.wantName)
			}
		})
	}
}

func TestFixSinaPseudoJSONKeepsValidObjectArrayCommas(t *testing.T) {
	input := `[{"symbol":"sh600000","code":"600000","name":"浦发银行"},{"symbol":"sh600004","code":"600004","name":"白云机场"}]`
	fixed := fixSinaPseudoJSON([]byte(input))
	var rows []struct {
		Code string `json:"code"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(fixed, &rows); err != nil {
		t.Fatalf("unmarshal fixed response: %v; fixed=%s", err, fixed)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2; fixed=%s", len(rows), fixed)
	}
	if rows[1].Code != "600004" || rows[1].Name != "白云机场" {
		t.Fatalf("second row = %+v", rows[1])
	}
}

func TestNormalizeSinaUniverseSymbol(t *testing.T) {
	tests := []struct {
		name       string
		code       string
		symbol     string
		fallback   int
		wantSymbol string
		wantMarket int
	}{
		{name: "uses code and market prefix", code: "600000", symbol: "sh600000", fallback: 0, wantSymbol: "600000", wantMarket: 1},
		{name: "uses prefixed symbol when code empty", code: "", symbol: "sz000001", fallback: 1, wantSymbol: "000001", wantMarket: 0},
		{name: "strips prefix from code", code: "bj430047", symbol: "", fallback: 1, wantSymbol: "430047", wantMarket: 2},
		{name: "keeps fallback market", code: "300750", symbol: "", fallback: 0, wantSymbol: "300750", wantMarket: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			symbol, market := normalizeSinaUniverseSymbol(tt.code, tt.symbol, tt.fallback)
			if symbol != tt.wantSymbol || market != tt.wantMarket {
				t.Fatalf("normalize = (%s, %d), want (%s, %d)", symbol, market, tt.wantSymbol, tt.wantMarket)
			}
		})
	}
}
