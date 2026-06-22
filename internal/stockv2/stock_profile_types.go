package stockv2

import (
	"errors"
	"time"
)

var ErrStockProfileNotFound = errors.New("stock profile not found")

// StockProfile 是消息面高召回关联使用的静态文本资产。
// 它只来自标的主数据/基金元数据,不包含组合、成本、仓位和风险偏好等动态上下文。
type StockProfile struct {
	Symbol          string    `json:"symbol"`
	Market          string    `json:"market"`
	InstrumentType  string    `json:"instrumentType"`
	Name            string    `json:"name"`
	Aliases         []string  `json:"aliases"`
	Industry        string    `json:"industry,omitempty"`
	Sectors         []string  `json:"sectors"`
	Concepts        []string  `json:"concepts"`
	Tags            []string  `json:"tags"`
	BusinessSummary string    `json:"businessSummary,omitempty"`
	ProfileText     string    `json:"profileText"`
	FundType        string    `json:"fundType,omitempty"`
	TrackingIndex   string    `json:"trackingIndex,omitempty"`
	Theme           string    `json:"theme,omitempty"`
	ConstituentHint string    `json:"constituentHint,omitempty"`
	ProfileVersion  int       `json:"profileVersion"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

type StockProfileListFilter struct {
	Market         string
	InstrumentType string
	Keyword        string
	Limit          int
	Offset         int
}

type RebuildStockProfilesResult struct {
	Total       int             `json:"total"`
	Success     int             `json:"success"`
	Failed      int             `json:"failed"`
	FailedItems []UpdateFailure `json:"failedItems,omitempty"`
	UpdatedAt   time.Time       `json:"updatedAt"`
}
