package stockv2

import (
	"errors"
	"time"
)

// 监控任务是系统固化的后台监控行为,不是用户创建/编辑/删除的业务对象。
// 用户只能配置开关 / 周期 / 范围 / 敏感度 / 冷却 / Agent doublecheck 开关。
// 本轮:quote/data_strategy/portfolio_risk/news 可运行;
// fundamental/data_quality 仅占位(Runnable=false),不假装实现。

const (
	MonitorTaskLatestQuoteRefresh      = "latest_quote_refresh"
	MonitorTaskDataStrategyMonitor     = "data_strategy_monitor"
	MonitorTaskPortfolioRiskMonitor    = "portfolio_risk_monitor"
	MonitorTaskNewsStrategyMonitor     = "news_strategy_monitor"
	MonitorTaskPortfolioSentinel       = AgentTaskTypePortfolioSentinel
	MonitorTaskDailyFundamentalMonitor = "daily_fundamental_monitor"
	MonitorTaskDataQualityMonitor      = "data_quality_monitor"
)

const (
	MonitorRunStatusRunning   = "running"
	MonitorRunStatusCompleted = "completed"
	MonitorRunStatusFailed    = "failed"
	MonitorRunStatusCancelled = "cancelled"
)

const (
	MonitorTriggerManual    = "manual"
	MonitorTriggerScheduled = "scheduled"
	MonitorTriggerEvent     = "event"
)

// MonitorHit 命中候选状态。candidate→(可选 doublecheck)→alerted;reviewed/ignored 为终态。
const (
	MonitorHitStatusCandidate     = "candidate"
	MonitorHitStatusDoublechecked = "doublechecked"
	MonitorHitStatusAlerted       = "alerted"
	MonitorHitStatusReviewed      = "reviewed"
	MonitorHitStatusIgnored       = "ignored"
)

var (
	ErrMonitorTaskNotFound       = errors.New("monitor task not found")
	ErrMonitorHitNotFound        = errors.New("monitor hit not found")
	ErrMonitorTaskNotConfigured  = errors.New("monitor task not configured")
	ErrMonitorTaskAlreadyRunning = errors.New("monitor task already running")
	ErrInvalidMonitorTaskType    = errors.New("invalid monitor task type")
	ErrInvalidMonitorRunStatus   = errors.New("invalid monitor run status")
)

// MonitorTaskDefinition 系统内置任务定义(代码常量,非表)。
type MonitorTaskDefinition struct {
	TaskType      string            `json:"taskType"`
	Label         string            `json:"label"`
	Description   string            `json:"description,omitempty"`
	Category      string            `json:"category"` // data | strategy | portfolio | news | fundamental | quality
	Runnable      bool              `json:"runnable"`
	Configurable  bool              `json:"configurable"`
	DefaultConfig MonitorTaskConfig `json:"defaultConfig"`
}

// MonitorTaskConfig 任务配置(可修改并持久化)。
type MonitorTaskConfig struct {
	Enabled                 bool   `json:"enabled"`
	IntervalSeconds         int    `json:"intervalSeconds"`
	Scope                   string `json:"scope,omitempty"`
	Sensitivity             string `json:"sensitivity,omitempty"`
	CooldownSeconds         int    `json:"cooldownSeconds"`
	AgentDoublecheckEnabled bool   `json:"agentDoublecheckEnabled"`
	AgentBudget             int    `json:"agentBudget,omitempty"`
}

// MonitorTask 聚合视图:定义 + 当前配置 + 最近一次运行摘要。
type MonitorTask struct {
	Definition MonitorTaskDefinition `json:"definition"`
	Config     MonitorTaskConfig     `json:"config"`
	LatestRun  *MonitorRun           `json:"latestRun,omitempty"`
}

// MonitorRun 一次后台监控任务执行。
type MonitorRun struct {
	ID           string         `json:"id"`
	TaskType     string         `json:"taskType"`
	Status       string         `json:"status"`
	TriggerType  string         `json:"triggerType"`
	StartedAt    time.Time      `json:"startedAt"`
	FinishedAt   time.Time      `json:"finishedAt,omitempty"`
	ScopeSummary string         `json:"scopeSummary,omitempty"`
	ScannedCount int            `json:"scannedCount"`
	HitCount     int            `json:"hitCount"`
	AlertCount   int            `json:"alertCount"`
	ReviewCount  int            `json:"reviewCount,omitempty"`
	SuccessCount int            `json:"successCount"`
	FailedCount  int            `json:"failedCount"`
	ErrorMessage string         `json:"errorMessage,omitempty"`
	Metadata     map[string]any `json:"metadata,omitempty"`
	CreatedAt    time.Time      `json:"createdAt"`
}

// MonitorHit 一次命中候选(规则命中但尚未成为买卖建议)。
type MonitorHit struct {
	ID              string         `json:"id"`
	RunID           string         `json:"runId"`
	TaskType        string         `json:"taskType"`
	Status          string         `json:"status"`
	StrategyID      string         `json:"strategyId,omitempty"`
	PortfolioID     string         `json:"portfolioId,omitempty"`
	Symbol          string         `json:"symbol,omitempty"`
	Market          string         `json:"market,omitempty"`
	Title           string         `json:"title"`
	Summary         string         `json:"summary,omitempty"`
	Evidence        map[string]any `json:"evidence,omitempty"`
	AgentDecisionID string         `json:"agentDecisionId,omitempty"`
	AlertID         string         `json:"alertId,omitempty"`
	CreatedAt       time.Time      `json:"createdAt"`
}

type RequestUpdateMonitorTaskConfig struct {
	Enabled                 *bool   `json:"enabled,omitempty"`
	IntervalSeconds         *int    `json:"intervalSeconds,omitempty"`
	Scope                   *string `json:"scope,omitempty"`
	Sensitivity             *string `json:"sensitivity,omitempty"`
	CooldownSeconds         *int    `json:"cooldownSeconds,omitempty"`
	AgentDoublecheckEnabled *bool   `json:"agentDoublecheckEnabled,omitempty"`
	AgentBudget             *int    `json:"agentBudget,omitempty"`
}

type MonitorRunListFilter struct {
	TaskType string
	Status   string
	Limit    int
	Offset   int
}

type MonitorHitListFilter struct {
	RunID       string
	TaskType    string
	Status      string
	StrategyID  string
	PortfolioID string
	Symbol      string
	Limit       int
	Offset      int
}

// builtinMonitorTaskDefinitions 返回系统内置监控任务定义注册表。
func builtinMonitorTaskDefinitions() []MonitorTaskDefinition {
	return []MonitorTaskDefinition{
		{
			TaskType:     MonitorTaskLatestQuoteRefresh,
			Label:        "盘中分钟行情",
			Description:  "持仓与监控标的的分钟级行情快照采集,并驱动组合估值刷新",
			Category:     "data",
			Runnable:     true,
			Configurable: true,
			DefaultConfig: MonitorTaskConfig{
				Enabled: true, IntervalSeconds: 30, Sensitivity: "normal",
			},
		},
		{
			TaskType:     MonitorTaskPortfolioSentinel,
			Label:        "组合哨兵",
			Description:  "窗口级扫描组合信息面与数据面,由 Agent 判断风险并派生 Review",
			Category:     "portfolio",
			Runnable:     false,
			Configurable: false,
			DefaultConfig: MonitorTaskConfig{
				Enabled: false, IntervalSeconds: 14400, Sensitivity: "normal", CooldownSeconds: 3600, AgentDoublecheckEnabled: true,
			},
		},
		{
			TaskType:     MonitorTaskDataStrategyMonitor,
			Label:        "数据面策略监控",
			Description:  "扫描 active 策略的触发规则；价格与涨跌幅越线由分钟行情刷新即时复查，其余条件按配置周期扫描",
			Category:     "strategy",
			Runnable:     true,
			Configurable: true,
			DefaultConfig: MonitorTaskConfig{
				Enabled: false, IntervalSeconds: 600, Sensitivity: "normal", CooldownSeconds: 1800,
			},
		},
		{
			TaskType:     MonitorTaskPortfolioRiskMonitor,
			Label:        "组合风险监控",
			Description:  "扫描组合快照与持仓,检查单票权重与数据新鲜度",
			Category:     "portfolio",
			Runnable:     true,
			Configurable: true,
			DefaultConfig: MonitorTaskConfig{
				Enabled: false, IntervalSeconds: 600, Sensitivity: "normal", CooldownSeconds: 1800,
			},
		},
		{
			TaskType:     MonitorTaskNewsStrategyMonitor,
			Label:        "消息面策略监控",
			Description:  "扫描 NewsLinkCandidate,对持仓/活跃策略/高分消息生成 MonitorHit",
			Category:     "news",
			Runnable:     true,
			Configurable: true,
			DefaultConfig: MonitorTaskConfig{
				Enabled: false, IntervalSeconds: 600, Sensitivity: "normal", CooldownSeconds: 3600,
			},
		},
		{
			TaskType:     MonitorTaskDailyFundamentalMonitor,
			Label:        "每日基本面监控",
			Description:  "基本面变动监控(本轮未实现,仅占位)",
			Category:     "fundamental",
			Runnable:     false,
			Configurable: true,
			DefaultConfig: MonitorTaskConfig{
				Enabled: false, IntervalSeconds: 86400, CooldownSeconds: 3600,
			},
		},
		{
			TaskType:     MonitorTaskDataQualityMonitor,
			Label:        "数据质量监控",
			Description:  "数据质量与新鲜度巡检(本轮未实现,仅占位)",
			Category:     "quality",
			Runnable:     false,
			Configurable: true,
			DefaultConfig: MonitorTaskConfig{
				Enabled: false, IntervalSeconds: 3600, CooldownSeconds: 3600,
			},
		},
	}
}

func monitorTaskDefinition(taskType string) (MonitorTaskDefinition, bool) {
	for _, def := range builtinMonitorTaskDefinitions() {
		if def.TaskType == taskType {
			return def, true
		}
	}
	return MonitorTaskDefinition{}, false
}

func validMonitorRunStatus(status string) bool {
	return status == MonitorRunStatusRunning ||
		status == MonitorRunStatusCompleted ||
		status == MonitorRunStatusFailed ||
		status == MonitorRunStatusCancelled
}
