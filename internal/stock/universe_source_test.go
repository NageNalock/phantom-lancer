package stock

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"phantom-lancer/internal/storage"
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

func TestConvertInstrumentsUsesListedLifecycleStatus(t *testing.T) {
	items := convertInstruments([]universeInstrument{{
		Symbol:       "600000",
		RemoteMarket: 1,
		Name:         "浦发银行",
	}}, nil)
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	if items[0].Status != "listed" {
		t.Fatalf("status = %q, want listed", items[0].Status)
	}
}

func TestRefreshAStockUniverseSkipsDelistWhenEastmoneyPartial(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "phantom-lancer.db"), nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	if _, err := store.UpsertStockInstrument(ctx, storage.StockInstrument{
		Symbol: "000001",
		Market: "SZ",
		Name:   "本地旧股票",
		Status: "listed",
	}); err != nil {
		t.Fatalf("seed old instrument: %v", err)
	}

	svc := NewService(store, nil)
	svc.client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		host := req.URL.Host
		// sina money.finance 是当前唯一 universe 源
		if !strings.HasPrefix(host, "money.finance.sina") {
			return stringResponse(http.StatusNotFound, `{"error":"unexpected host "+host}`), nil
		}
		node := req.URL.Query().Get("node")
		// 关键：sina universe 3 节点中 sz_a 返回 502（模拟部分失败 degraded）
		if node == "sz_a" {
			return stringResponse(http.StatusBadGateway, `bad gateway`), nil
		}
		// sh_a 返回 1500 条（从 600000 起），bj_a 返回 500 条（从 430001 起）
		switch node {
		case "sh_a":
			return stringResponse(http.StatusOK, sinaUniverseNode(600000, 1500, "sh")), nil
		case "bj_a":
			return stringResponse(http.StatusOK, sinaUniverseNode(430001, 500, "bj")), nil
		default:
			return stringResponse(http.StatusOK, `[]`), nil
		}
	})}

	result, err := svc.RefreshAStockUniverse(ctx, MaintenanceModeManual, "test")
	if err != nil {
		t.Fatalf("refresh universe: %v", err)
	}
	// sz_a 节点 502 → degraded（有 notes，且整体任务仍成功）
	if result.Task.Status != "degraded" {
		t.Fatalf("task status = %q, want degraded (sz_a node failed)", result.Task.Status)
	}
	old, err := store.GetStockInstrument(ctx, "000001")
	if err != nil {
		t.Fatalf("get old instrument: %v", err)
	}
	if old.Status != "listed" {
		t.Fatalf("old instrument status = %q, want listed because remote fetch was partial", old.Status)
	}
	if !containsNote(result.Notes, "sz_a") {
		t.Fatalf("notes = %#v, want sz_a fetch note", result.Notes)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func stringResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func eastmoneyUniversePage(total, start, count int) string {
	rows := make([]string, 0, count)
	for i := 0; i < count; i++ {
		symbol := fmt.Sprintf("%06d", start+i)
		rows = append(rows, fmt.Sprintf(`{"f12":"%s","f13":1,"f14":"测试%s","f18":"","f100":""}`, symbol, symbol))
	}
	return fmt.Sprintf(`{"data":{"total":%d,"diff":[%s]}}`, total, strings.Join(rows, ","))
}

// sinaUniverseNode 生成新浪 money.finance getHQNodeData 风格的 node 响应（伪 JSON：键不加引号）。
// market 是 "sh" / "sz" / "bj"，symbol 前缀依此生成。
func sinaUniverseNode(startCode, count int, market string) string {
	prefix := market
	rows := make([]string, 0, count)
	for i := 0; i < count; i++ {
		code := fmt.Sprintf("%06d", startCode+i)
		rows = append(rows, fmt.Sprintf(
			`{code:"%s",symbol:"%s%s",name:"股票-%s",trade:"1"}`,
			code, prefix, code, code,
		))
	}
	return "[" + strings.Join(rows, ",") + "]"
}

func containsNote(notes []string, needle string) bool {
	for _, note := range notes {
		if strings.Contains(note, needle) {
			return true
		}
	}
	return false
}
