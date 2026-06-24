package stockv2

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	stockProfileF10Timeout      = 8 * time.Second
	stockProfileF10MaxBodyBytes = 3 << 20
)

func (s *Service) enrichStockProfileFromPublicSources(ctx context.Context, profile StockProfile, instrument StockV2Instrument) (StockProfile, []StockProfileSourceStatus) {
	if s == nil || s.httpClient == nil {
		return profile, []StockProfileSourceStatus{stockProfileSourceStatus("public_f10", StockProfileSourceStatusSkipped, "http client unavailable")}
	}
	ctx, cancel := context.WithTimeout(ctx, stockProfileF10Timeout)
	defer cancel()

	statuses := make([]StockProfileSourceStatus, 0, 3)
	if instrument.InstrumentType == InstrumentTypeExchangeFund || looksLikeExchangeFund(instrument.Name) {
		statuses = append(statuses, stockProfileSourceStatusFromError("eastmoney_fund_basic", s.enrichFundProfileBasics(ctx, &profile)))
		statuses = append(statuses, stockProfileSourceStatusFromError("eastmoney_fund_holdings", s.enrichFundProfileHoldings(ctx, &profile)))
	} else {
		code := eastmoneyF10Code(profile.Market, profile.Symbol)
		if code == "" {
			statuses = append(statuses, stockProfileSourceStatus("eastmoney_f10", StockProfileSourceStatusSkipped, "empty code"))
		} else {
			statuses = append(statuses, stockProfileSourceStatusFromError("eastmoney_company_survey", s.enrichStockProfileCompanySurvey(ctx, &profile, code)))
			statuses = append(statuses, stockProfileSourceStatusFromError("eastmoney_business_analysis", s.enrichStockProfileBusinessAnalysis(ctx, &profile, code)))
			statuses = append(statuses, stockProfileSourceStatusFromError("eastmoney_core_conception", s.enrichStockProfileCoreConception(ctx, &profile, code)))
		}
	}
	if profile.BusinessSummaryZh != "" {
		profile.BusinessSummary = profile.BusinessSummaryZh
	}
	profile.Aliases = appendProfileTerms(profile.Aliases, profile.AliasesZh...)
	profile.Aliases = appendProfileTerms(profile.Aliases, profile.AliasesEn...)
	profile.ProfileTextZh = buildProfileTextZh(profile)
	profile.ProfileTextEn = buildProfileTextEn(profile)
	profile.ProfileText = buildProfileText(profile)
	return profile, statuses
}

func (s *Service) enrichStockProfileFromEastmoney(ctx context.Context, profile *StockProfile) error {
	code := eastmoneyF10Code(profile.Market, profile.Symbol)
	if code == "" {
		return nil
	}
	var firstErr error
	if err := s.enrichStockProfileCompanySurvey(ctx, profile, code); err != nil {
		firstErr = err
	}
	if err := s.enrichStockProfileBusinessAnalysis(ctx, profile, code); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := s.enrichStockProfileCoreConception(ctx, profile, code); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

func (s *Service) enrichStockProfileCompanySurvey(ctx context.Context, profile *StockProfile, code string) error {
	var payload eastmoneyCompanySurveyResponse
	if err := s.fetchStockProfileJSON(ctx, "https://emweb.securities.eastmoney.com/PC_HSF10/CompanySurvey/CompanySurveyAjax?code="+url.QueryEscape(code), &payload); err != nil {
		return err
	}
	item := payload.Company
	if strings.TrimSpace(item.Name) != "" {
		profile.AliasesZh = appendProfileTerms(profile.AliasesZh, item.Name)
	}
	if strings.TrimSpace(item.Abbr) != "" {
		profile.AliasesZh = appendProfileTerms(profile.AliasesZh, item.Abbr)
	}
	if strings.TrimSpace(item.EnglishName) != "" {
		profile.AliasesEn = appendProfileTerms(profile.AliasesEn, item.EnglishName)
	}
	if profile.Industry == "" {
		profile.Industry = firstNonEmpty(item.Industry, item.CSRCIndustry)
	}
	profile.Tags = appendProfileTerms(profile.Tags, item.Industry, item.CSRCIndustry)
	profile.KeywordsZh = appendProfileTerms(profile.KeywordsZh, item.Industry, item.CSRCIndustry)
	if summary := stockProfileSnippet(item.Summary, 520); summary != "" {
		profile.BusinessSummaryZh = summary
	}
	profile.BusinessLinesZh = appendProfileTerms(profile.BusinessLinesZh, stockProfileTermsFromText(item.BusinessScope, 12)...)
	profile.KeywordsZh = appendProfileTerms(profile.KeywordsZh, profile.BusinessLinesZh...)
	return nil
}

func (s *Service) enrichStockProfileBusinessAnalysis(ctx context.Context, profile *StockProfile, code string) error {
	var payload eastmoneyBusinessAnalysisResponse
	if err := s.fetchStockProfileJSON(ctx, "https://emweb.securities.eastmoney.com/PC_HSF10/BusinessAnalysis/PageAjax?code="+url.QueryEscape(code), &payload); err != nil {
		return err
	}
	lines := latestBusinessLines(payload.BusinessItems, 8)
	profile.BusinessLinesZh = appendProfileTerms(profile.BusinessLinesZh, lines...)
	profile.KeywordsZh = appendProfileTerms(profile.KeywordsZh, lines...)
	if profile.BusinessSummaryZh == "" && len(lines) > 0 {
		profile.BusinessSummaryZh = fmt.Sprintf("%s主营业务包括%s。", profile.Name, strings.Join(lines, "、"))
	}
	return nil
}

func (s *Service) enrichStockProfileCoreConception(ctx context.Context, profile *StockProfile, code string) error {
	var payload eastmoneyCoreConceptionResponse
	if err := s.fetchStockProfileJSON(ctx, "https://emweb.securities.eastmoney.com/PC_HSF10/CoreConception/PageAjax?code="+url.QueryEscape(code), &payload); err != nil {
		return err
	}
	for _, board := range payload.Boards {
		name := strings.TrimSpace(board.BoardName)
		if name == "" || stockProfileConceptLooksNoisy(name) {
			continue
		}
		profile.Concepts = appendProfileTerms(profile.Concepts, name)
		profile.Tags = appendProfileTerms(profile.Tags, name)
		profile.KeywordsZh = appendProfileTerms(profile.KeywordsZh, name)
	}
	for _, topic := range payload.Topics {
		keyword := strings.TrimSpace(topic.Keyword)
		if keyword == "" {
			continue
		}
		switch strings.TrimSpace(topic.Class) {
		case "主营业务":
			profile.BusinessLinesZh = appendProfileTerms(profile.BusinessLinesZh, keyword)
		case "行业背景", "核心竞争力":
			profile.KeywordsZh = appendProfileTerms(profile.KeywordsZh, keyword)
		default:
			profile.KeywordsZh = appendProfileTerms(profile.KeywordsZh, keyword)
		}
	}
	return nil
}

func (s *Service) enrichFundProfileFromEastmoney(ctx context.Context, profile *StockProfile) error {
	var firstErr error
	if err := s.enrichFundProfileBasics(ctx, profile); err != nil {
		firstErr = err
	}
	if err := s.enrichFundProfileHoldings(ctx, profile); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

func (s *Service) enrichFundProfileBasics(ctx context.Context, profile *StockProfile) error {
	body, err := s.fetchStockProfileText(ctx, "https://fundf10.eastmoney.com/jbgk_"+url.PathEscape(profile.Symbol)+".html")
	if err != nil {
		return err
	}
	fullName := stockProfileHTMLCell(body, "基金全称")
	if fullName != "" {
		profile.AliasesZh = appendProfileTerms(profile.AliasesZh, fullName)
	}
	if fundType := stockProfileHTMLCell(body, "基金类型"); fundType != "" {
		profile.FundType = fundType
		profile.Tags = appendProfileTerms(profile.Tags, fundType)
		profile.KeywordsZh = appendProfileTerms(profile.KeywordsZh, fundType)
	}
	if benchmark := stockProfileHTMLCell(body, "业绩比较基准"); benchmark != "" {
		profile.KeywordsZh = appendProfileTerms(profile.KeywordsZh, stockProfileTermsFromText(benchmark, 8)...)
	}
	target := stockProfileHTMLSection(body, "投资目标")
	rangeText := stockProfileHTMLSection(body, "投资范围")
	risk := stockProfileHTMLSection(body, "风险收益特征")
	profile.BusinessLinesZh = appendProfileTerms(profile.BusinessLinesZh, stockProfileTermsFromText(target, 8)...)
	profile.BusinessLinesZh = appendProfileTerms(profile.BusinessLinesZh, stockProfileTermsFromText(rangeText, 8)...)
	profile.RiskTagsZh = appendProfileTerms(profile.RiskTagsZh, stockProfileTermsFromText(risk, 6)...)
	if target != "" || rangeText != "" {
		profile.BusinessSummaryZh = stockProfileSnippet(strings.Join(cleanProfileTerms([]string{
			profile.Name,
			"基金类型:" + profile.FundType,
			"投资目标:" + target,
			"投资范围:" + rangeText,
		}), "。"), 520)
	}
	return nil
}

func (s *Service) enrichFundProfileHoldings(ctx context.Context, profile *StockProfile) error {
	values := url.Values{}
	values.Set("type", "jjcc")
	values.Set("code", profile.Symbol)
	values.Set("topline", "10")
	values.Set("year", "")
	values.Set("month", "")
	values.Set("rt", "0")
	body, err := s.fetchStockProfileText(ctx, "https://fundf10.eastmoney.com/FundArchivesDatas.aspx?"+values.Encode())
	if err != nil {
		return err
	}
	holdings := parseFundHoldingNames(body, 10)
	if len(holdings) == 0 {
		return nil
	}
	profile.ConstituentHint = "前十大持仓: " + strings.Join(holdings, "、")
	profile.KeywordsZh = appendProfileTerms(profile.KeywordsZh, holdings...)
	profile.Tags = appendProfileTerms(profile.Tags, "重仓股")
	return nil
}

func (s *Service) fetchStockProfileJSON(ctx context.Context, endpoint string, target any) error {
	body, err := s.fetchStockProfileBody(ctx, endpoint)
	if err != nil {
		return err
	}
	return json.Unmarshal(body, target)
}

func (s *Service) fetchStockProfileText(ctx context.Context, endpoint string) (string, error) {
	body, err := s.fetchStockProfileBody(ctx, endpoint)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func (s *Service) fetchStockProfileBody(ctx context.Context, endpoint string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json,text/plain,text/html,*/*")
	req.Header.Set("User-Agent", "phantom-lancer-stockv2/1.0")
	req.Header.Set("Referer", "https://quote.eastmoney.com/")
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, stockProfileF10MaxBodyBytes+1))
	if readErr != nil {
		return nil, readErr
	}
	if len(body) > stockProfileF10MaxBodyBytes {
		return nil, fmt.Errorf("stock profile source response too large")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("stock profile source returned %d", resp.StatusCode)
	}
	if stockProfileResponseLooksHTML(body) && !strings.Contains(endpoint, "fundf10.eastmoney.com") {
		return nil, fmt.Errorf("stock profile source returned html")
	}
	return body, nil
}

type eastmoneyCompanySurveyResponse struct {
	Company eastmoneyCompanySurvey `json:"jbzl"`
}

type eastmoneyCompanySurvey struct {
	Name          string `json:"gsmc"`
	Abbr          string `json:"agjc"`
	EnglishName   string `json:"ywmc"`
	Industry      string `json:"sshy"`
	CSRCIndustry  string `json:"sszjhhy"`
	Summary       string `json:"gsjj"`
	BusinessScope string `json:"jyfw"`
}

type eastmoneyBusinessAnalysisResponse struct {
	BusinessItems []eastmoneyBusinessItem `json:"zygcfx"`
}

type eastmoneyBusinessItem struct {
	ReportDate string `json:"REPORT_DATE"`
	MainOpType string `json:"MAINOP_TYPE"`
	ItemName   string `json:"ITEM_NAME"`
	Ratio      string `json:"MBI_RATIO"`
}

type eastmoneyCoreConceptionResponse struct {
	Boards []eastmoneyBoard     `json:"ssbk"`
	Topics []eastmoneyCoreTopic `json:"hxtc"`
}

type eastmoneyBoard struct {
	BoardName string `json:"BOARD_NAME"`
}

type eastmoneyCoreTopic struct {
	Keyword string `json:"KEYWORD"`
	Class   string `json:"KEY_CLASSIF"`
}

func eastmoneyF10Code(market, symbol string) string {
	symbol = strings.TrimSpace(symbol)
	if symbol == "" {
		return ""
	}
	market = strings.ToUpper(strings.TrimSpace(market))
	if market == "" {
		market = inferAStockMarket(symbol)
	}
	switch market {
	case "SH", "SZ", "BJ":
		return market + symbol
	default:
		return symbol
	}
}

func latestBusinessLines(items []eastmoneyBusinessItem, maxItems int) []string {
	latestDate := ""
	for _, item := range items {
		if strings.TrimSpace(item.ItemName) == "" {
			continue
		}
		if latestDate == "" || item.ReportDate > latestDate {
			latestDate = item.ReportDate
		}
	}
	type weighted struct {
		name  string
		ratio float64
	}
	weightedItems := make([]weighted, 0)
	for _, item := range items {
		name := strings.TrimSpace(item.ItemName)
		if name == "" || item.ReportDate != latestDate {
			continue
		}
		if item.MainOpType != "" && item.MainOpType != "2" {
			continue
		}
		ratio, _ := strconv.ParseFloat(strings.TrimSpace(item.Ratio), 64)
		weightedItems = append(weightedItems, weighted{name: name, ratio: ratio})
	}
	sort.SliceStable(weightedItems, func(i, j int) bool {
		return weightedItems[i].ratio > weightedItems[j].ratio
	})
	out := make([]string, 0, min(len(weightedItems), maxItems))
	for _, item := range weightedItems {
		out = append(out, item.name)
		if len(out) >= maxItems {
			break
		}
	}
	return cleanProfileTerms(out)
}

func stockProfileTermsFromText(text string, maxItems int) []string {
	text = stockProfileStripHTML(text)
	if text == "" {
		return nil
	}
	fields := strings.FieldsFunc(text, func(r rune) bool {
		return r == ',' || r == '，' || r == ';' || r == '；' || r == '、' || r == ':' || r == '：' || r == '\n' || r == '\r' || r == '(' || r == ')' || r == '（' || r == '）'
	})
	out := make([]string, 0, min(len(fields), maxItems))
	for _, field := range fields {
		term := stockProfileSnippet(field, 32)
		if len([]rune(term)) < 2 || stockProfileTermLooksGeneric(term) {
			continue
		}
		out = append(out, term)
		if len(out) >= maxItems {
			break
		}
	}
	return cleanProfileTerms(out)
}

func stockProfileHTMLCell(body, label string) string {
	re, err := regexp.Compile(`(?is)<th[^>]*>\s*` + regexp.QuoteMeta(label) + `\s*</th>\s*<td[^>]*>(.*?)</td>`)
	if err != nil {
		return ""
	}
	matches := re.FindStringSubmatch(body)
	if len(matches) < 2 {
		return ""
	}
	return stockProfileSnippet(matches[1], 240)
}

func stockProfileHTMLSection(body, label string) string {
	re, err := regexp.Compile(`(?is)<label[^>]*>\s*` + regexp.QuoteMeta(label) + `\s*</label>.*?<p[^>]*>(.*?)</p>`)
	if err != nil {
		return ""
	}
	matches := re.FindStringSubmatch(body)
	if len(matches) < 2 {
		return ""
	}
	return stockProfileSnippet(matches[1], 360)
}

func parseFundHoldingNames(body string, maxItems int) []string {
	re := regexp.MustCompile(`(?is)<td[^>]*class=['"]?tol['"]?[^>]*>.*?<a[^>]*>([^<]+)</a>`)
	matches := re.FindAllStringSubmatch(body, -1)
	out := make([]string, 0, min(len(matches), maxItems))
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		name := stockProfileSnippet(match[1], 24)
		if name == "" || strings.Contains(name, "股票名称") {
			continue
		}
		out = append(out, name)
		if len(out) >= maxItems {
			break
		}
	}
	return cleanProfileTerms(out)
}

func stockProfileSnippet(text string, maxRunes int) string {
	text = stockProfileStripHTML(text)
	if text == "" || maxRunes <= 0 {
		return text
	}
	runes := []rune(text)
	if len(runes) <= maxRunes {
		return text
	}
	return string(runes[:maxRunes]) + "..."
}

func stockProfileStripHTML(text string) string {
	text = html.UnescapeString(text)
	text = strings.ReplaceAll(text, "\u00a0", " ")
	text = regexp.MustCompile(`(?is)<br\s*/?>`).ReplaceAllString(text, "、")
	text = regexp.MustCompile(`(?is)<[^>]+>`).ReplaceAllString(text, " ")
	return strings.Join(strings.Fields(text), " ")
}

func stockProfileResponseLooksHTML(body []byte) bool {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return false
	}
	lower := strings.ToLower(string(trimmed[:min(len(trimmed), 32)]))
	return strings.HasPrefix(lower, "<!doctype html") || strings.HasPrefix(lower, "<html")
}

func stockProfileTermLooksGeneric(term string) bool {
	switch strings.TrimSpace(term) {
	case "公司", "生产", "销售", "服务", "投资", "管理", "产品", "业务", "无":
		return true
	default:
		return false
	}
}

func stockProfileConceptLooksNoisy(name string) bool {
	for _, marker := range []string{"融资融券", "深股通", "沪股通", "MSCI", "富时", "标准普尔", "机构重仓", "百元股", "大盘股", "创业板综", "深证", "证金持股"} {
		if strings.Contains(name, marker) {
			return true
		}
	}
	return false
}

func stockProfileSourceStatusFromError(source string, err error) StockProfileSourceStatus {
	if err == nil {
		return stockProfileSourceStatus(source, StockProfileSourceStatusSuccess, "")
	}
	return stockProfileSourceStatus(source, StockProfileSourceStatusFailed, stockProfileSnippet(err.Error(), 240))
}

func stockProfileSourceStatus(source, status, message string) StockProfileSourceStatus {
	return StockProfileSourceStatus{
		Source:    source,
		Status:    status,
		Message:   message,
		FetchedAt: time.Now(),
	}
}
