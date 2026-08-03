# 股票 V2 组合哨兵能力设计

> 文档日期：2026-07-02
>
> 状态：V2 开发前设计
>
> REPLACES：无。本文补充 `stock-agent-workbench-v2-key-points-2026-06-18.md` 中组合风险、消息面、Review 与 Agent 留痕链路。
>
> 相关文档：`docs/stock-v2-strategy-generation-design-2026-06-26.md` 定义策略生成和 `OperationReview` 进入路径；`docs/stock-v2-opportunity-discovery-technical-design-2026-06-26.md` 定义 Agent 研究执行、MCP 查询和可观测性模式。

## 2026-07-30 实现更新：持仓操作计划 v2

原 `portfolio-sentinel-report/v1` 仅保存风险结论并按需派生 Review，不能形成可持续监控的持仓处置方案。当前实现升级为以下约束；本文后续 v1 章节保留为历史设计背景：

- 新运行固定使用 `portfolio-sentinel-report/v2`；历史 v1 结果继续只读展示。
- `portfolio_sentinel` 整条执行链固定为 Codex CLI，不允许 API/DeepSeek 执行或降级。服务以 `codex --search exec` 启用实时公开检索，并在 Agent 账本中只保留搜索、MCP、Agent 工具调用次数与名称，不保存查询参数或响应正文。
- 每个当前持仓必须有且仅有一条 `action_plans[]`，动作限定为建仓、加仓、持有、减仓、清仓。非持仓建仓只能来自已入选机会候选或 active 单票策略形成的可信候选池。
- 可操作计划必须引用真实 `web_search` 或命名 search/research/browse Agent 工具对应的 `research_audit`；没有观察到真实公开检索时只允许一次纠正重试，仍不满足则整次运行失败。
- 条件计划只使用现有 watch evaluator 支持的价格、涨跌幅、日收盘价和组合权重确定性条件。一个成功运行以同一事务替换该组合的哨兵策略版本，最长有效七天。
- 计划复用 active `portfolio_monitor` 策略和 `data_strategy_monitor`。条件命中后按当时可用数量或当时组合资产动态计算股数/金额，直接生成待 owner 确认的 `proposed_operation`；不再次调用模型，不自动下单。
- 前端组合哨兵默认展示当前计划及监控中、已触发、已生成提案、已过期状态；Agent 任务绑定页对该任务固定展示 CLI 模式。

## 1. 背景

2026-07-02，存储相关科技类股票出现普遍大跌，当前组合中的科技/AI 服务器相关持仓发生损失。事后检查运行数据发现：

- 2026-07-01 晚间已有消息记录显示“美股存储概念股下跌，闪迪、西部数据、美光科技明显下跌”。
- 当前库里已有持仓、行情、日 K、分钟线、新闻、股票画像和 embedding。
- 现有数据面策略监控没有生成任何 `MonitorHit`、`Alert` 或 `OperationReview`，确定性的报价过期/仓位上限检查也不能替代信息面研判。
- 当前 `OperationReview` 能承接单次监控命中后的操作复核，但它不是周期性组合巡检能力。

根因不是完全没有数据，而是缺少一个独立能力：

```text
固定时间窗口
-> 收集当前组合的消息面 + 数据面
-> 让 Agent 判断利好、利空、噪音和组合影响
-> 生成组合级风险结论
-> 必要时 fan-out 到单票 OperationReview
```

该能力命名为 **组合哨兵 / Portfolio Sentinel**。中文 UI 文案使用“组合哨兵”，内部稳定 key 使用 `portfolio_sentinel`。

## 2. 目标

组合哨兵解决的是：

> 用户已经有持仓，希望系统在开盘前、午间、收盘后/夜间等关键窗口主动复核这段时间的消息面和数据面，提前发现可能影响当前持仓的风险或机会，并给出可确认的操作策略。

第一版目标：

```text
后台定时 / 手动触发
-> PortfolioSentinelRun
-> PortfolioSentinelContext
-> Codex CLI Agent
-> portfolio-sentinel-report/v1
-> SentinelResult 历史
-> Alert / MonitorHit / OperationReview
-> guardrails / 人工确认 / 操作记录
```

第一版优先服务“避免当前持仓风险扩大”，不是全市场机会发现。

## 3. 命名与边界

### 3.1 为什么不叫 Review

当前代码里 `Review` 已经有明确含义：

- `OperationReview`：从一个 `MonitorHit` 进入的操作复核单。
- `operation_review` Agent task：对单个 `OperationReview` 做 Agent 判断。
- `news_event_review` 是消息脉络归纳使用的稳定任务 key，不是组合操作复核单。

组合哨兵不是单个命中的复核单，而是周期性组合巡检。为了避免混淆：

- 能力名称：组合哨兵。
- 内部 key：`portfolio_sentinel`。
- Agent task：`portfolio_sentinel`。
- 输出 schema：`portfolio-sentinel-report/v1`。
- UI 二级入口：`组合哨兵`。

### 3.2 与现有能力关系

组合哨兵不依赖以下监控任务开启：

- `data_strategy_monitor`

原因：

- 信息面由组合哨兵直接读取窗口内的消息关联候选和主题变化，不经过独立逐条监控任务。
- `data_strategy_monitor` 依赖 active 策略，不适合没有 active 策略或仅有 draft 策略的持仓。
- 报价过期和单票仓位上限由 Watch 规则与操作 guardrails 做确定性检查，不需要另一项同义的周期监控任务。
- 组合哨兵有独立配置、调度和历史；它不会在通用监控任务配置中再注册一份重复入口。

组合哨兵可以复用：

- 持仓、组合、交易记录。
- 最新行情、日 K、分钟线、组合快照。
- 新闻事件、原始新闻、股票画像、embedding。
- Agent provider/model/task profile、AgentRun、DecisionLedger、Codex CLI executor、MCP。
- `OperationReview`、`proposed_operation`、guardrails、accept/reject/defer。

## 4. 能力边界

### 4.1 允许

- 按窗口扫描当前 StockV2 组合持仓。
- 汇总窗口内消息面、行情和组合状态。
- 让 Agent 判断消息的利好、利空、不相关和不确定性。
- 生成组合级风险/机会摘要。
- 在需要操作时，为每个单票动作创建 `MonitorHit -> OperationReview`。
- 使用现有 guardrails 检查 proposed operation。
- 保存每次运行历史、Agent 留痕、结构化输出和派生对象链接。

### 4.2 禁止

- 不自动下单。
- 不直接修改持仓。
- 不直接接受 `OperationReview`。
- 不直接激活策略。
- 不读取 token、cookie、私有配置或本地敏感文件。
- 不把 embedding / 关键词召回结果当成事实；召回只是候选材料。
- 不依赖消息候选的逐条消费状态作为主链路。
- 不把多个单票操作塞进一条 `OperationReview`。

### 4.3 第一版不做

- 不做全市场主题机会发现。
- 不做复杂主题知识图谱。
- 不新增自动交易执行。
- 不强制要求所有持仓已有 active 策略。
- 不删除 `NewsLinkCandidate`。

## 5. 触发模式

组合哨兵支持两类入口：

### 5.1 手动触发

用于调试、复盘和临时风险检查。

请求参数建议：

```json
{
  "portfolioId": "optional; empty means all active portfolios",
  "windowType": "manual",
  "startAt": "optional RFC3339",
  "endAt": "optional RFC3339",
  "note": "optional user note"
}
```

如果未提供 `startAt/endAt`，默认取最近 12 小时。

### 5.2 后台定时

默认窗口：

| windowType | 建议时间 | 默认时间范围 | 重点 |
| --- | --- | --- | --- |
| `pre_market` | A 股开盘前 | 上次收盘后到当前 | 隔夜海外、盘前新闻、昨日收盘后消息 |
| `midday` | 午间休盘 | 上午开盘到当前 | 上午行情、午间公告/快讯 |
| `post_close` | A 股收盘后/夜间 | 当日开盘到当前 | 全天行情、收盘后公告、海外早盘线索 |

第一版可以用简单 wall-clock 调度，不需要完整交易所节假日日历。若当天非交易日，任务仍可运行，但报告中必须标注 `marketSession=closed_or_unknown`。

## 6. 数据模型

第一版建议新增独立表，避免把组合哨兵历史硬塞进 `stockv2_monitor_runs`。

### 6.1 stockv2_portfolio_sentinel_runs

表示一次组合哨兵运行。

```text
id
portfolio_id              // nullable; 空表示本次扫描多个组合
agent_run_id              // nullable
decision_ledger_id        // nullable
status                    // pending | running | completed | failed | cancelled
trigger_type              // manual | scheduled
window_type               // manual | pre_market | midday | post_close
window_start_at
window_end_at
scanned_portfolio_count
scanned_holding_count
news_event_count
raw_news_count
quote_count
daily_bar_symbol_count
minute_bar_symbol_count
result_risk_level         // none | info | warning | critical
generated_alert_count
generated_hit_count
generated_review_count
error_message
started_at
finished_at
created_at
updated_at
```

说明：

- `result_risk_level` 是运行最终摘要，不替代单条 Alert/Review 的状态。
- 运行失败必须保留错误摘要，不能静默吞掉。
- `agent_run_id` 为空时表示未进入 Agent，例如无持仓或前置数据构造失败。

### 6.2 stockv2_portfolio_sentinel_results

保存结构化报告和派生对象链接。

```text
id
run_id
schema_version            // portfolio-sentinel-report/v1
summary
risk_level                // none | info | warning | critical
raw_result_json
context_summary_json
derived_alert_ids_json
derived_monitor_hit_ids_json
derived_review_ids_json
created_at
```

说明：

- `raw_result_json` 保存 Agent 结构化输出。
- `context_summary_json` 只保存输入摘要、统计和关键窗口，不保存完整原始上下文。
- 完整 prompt / transcript 继续由 `AgentRun` / `DecisionLedger` 承载。

### 6.3 是否复用 monitor_runs

不作为主存储。可以在需要时派生创建 `MonitorHit`，但组合哨兵历史必须有自己的 run/result。原因：

- `monitor_runs` 面向内置 monitor，可观测字段不足以描述 Agent 输入窗口、context 裁剪、结构化报告和派生对象。
- 历史页面需要按窗口、风险级别、Agent 状态和派生 Review 检索。

## 7. Context Pack

新增 `PortfolioSentinelContext`。

```text
schemaVersion
runId
window
portfolios[]
news[]
marketData
strategies
recentReviews
recentTransactions
dataFreshness
contextStats
```

### 7.1 Window

```json
{
  "windowType": "pre_market",
  "triggerType": "scheduled",
  "startAt": "2026-07-01T15:00:00+08:00",
  "endAt": "2026-07-02T09:00:00+08:00",
  "market": "a_share",
  "marketSession": "pre_market"
}
```

### 7.2 Portfolio context

每个组合包含：

- portfolio id/name/risk settings。
- cash、maxSinglePositionPct、allowBuy/allowAdd/allowReduce/allowSell。
- 最新 snapshot。
- 当前 holdings。
- 每个 holding 的 quantity、availableQuantity、costPrice、lastPrice、marketValue、pnl、positionPct。

### 7.3 Market data context

每个持仓标的包含：

- latest quote。
- latest quote freshness。
- 近 3/5/20 日日 K 摘要。
- 窗口内分钟线摘要。
- 数据缺口，例如 quote stale、daily bar 缺失、minute pctChange 为 0 等。

第一版摘要字段建议：

```text
latestClose
latestPctChange
ret3d
ret5d
ret20d
rangeHigh20d
rangeLow20d
latestVolume
intradayHigh
intradayLow
intradayLast
intradayDirectionSummary
```

### 7.4 News context

组合哨兵从 `NewsLinkCandidate` 读取与持仓相关的窗口内消息，并使用标准消息补充标题、来源和时间。

裁剪策略：

1. 当前持仓名称、代码、别名命中。
2. 股票画像关键词命中。
3. 语义向量召回持仓画像相似新闻。
4. 来源重要性、标题中出现明显市场/行业/公司动作。
5. 最近消息优先。

第一版限制：

- 每个组合最多 80 条新闻进入 Agent。
- 每个持仓最多 20 条新闻。
- 全局重要新闻最多 30 条。
- 超限时保留最新、高相似、高来源质量的消息。

每条新闻必须保留：

```text
id
source
title
summary/snippet
eventAt
url
matchReason
matchedHoldingSymbols[]
retrievalMethod         // keyword | semantic | source_priority | manual
```

### 7.5 策略和历史上下文

包含：

- 当前持仓相关 active/draft 策略摘要。
- 最近 10 条 OperationReview 摘要。
- 最近 20 条操作记录摘要。
- 最近相关 Alert 摘要。

不要把全部历史塞进 prompt。

## 8. Agent 任务与输出

### 8.1 AgentTaskType

新增：

```go
AgentTaskTypePortfolioSentinel = "portfolio_sentinel"
```

同时：

- `knownAgentTaskType` 加入该类型。
- `executableAgentTaskType` 加入该类型。
- 默认 task profile seed 加入该类型。
- 前端 Agent 设置页可绑定模型。

### 8.2 Executor

现有 `executeAgentRun` 硬调用 `ExecuteOperationReview`，组合哨兵不能复用该路径。

需要新增：

```go
ExecutePortfolioSentinel(ctx, taskID string, pack PortfolioSentinelContext, modelName string)
buildPortfolioSentinelPrompt(taskID string, pack PortfolioSentinelContext, mcpURL string)
RunPortfolioSentinel(ctx, req RequestRunPortfolioSentinel)
executePortfolioSentinelRun(...)
finalizePortfolioSentinelRun(...)
```

第一版可以不重构全部 Agent 分发器；直接按 `strategy_generation` 的模式新增专用异步路径，减少波及面。

### 8.3 Output schema

Agent 必须通过 MCP `stock_agent.submit_result` 回填：

```json
{
  "taskID": "<task-id>",
  "taskType": "portfolio_sentinel",
  "result": {
    "outputType": "portfolio-sentinel-report/v1",
    "resultSummary": "窗口内组合风险摘要",
    "confidence": 0.72,
    "result": {
      "schema_version": "portfolio-sentinel-report/v1",
      "window": {},
      "overall_risk_level": "warning",
      "run_summary": {},
      "positive_items": [],
      "negative_items": [],
      "noise_items": [],
      "affected_holdings": [],
      "portfolio_actions": [],
      "review_requests": [],
      "data_quality_notes": [],
      "next_watch_focus": []
    }
  }
}
```

`validAgentTaskOutputType` 需要允许：

```text
taskType=portfolio_sentinel
outputType=portfolio-sentinel-report/v1
```

### 8.4 Report fields

`affected_holdings[]`：

```json
{
  "symbol": "601138",
  "market": "SH",
  "name": "工业富联",
  "impact": "negative",
  "risk_level": "warning",
  "reason": "海外存储股大跌叠加 AI 服务器链条暴露",
  "evidence_refs": ["news:event-id", "quote:601138"],
  "confidence": 0.7
}
```

`portfolio_actions[]`：

```json
{
  "action_type": "proposed_operation",
  "portfolio_id": "id-...",
  "symbol": "601138",
  "market": "SH",
  "operation": {
    "action": "reduce_position",
    "quantity": 100,
    "priceBasis": "latest_quote"
  },
  "reason": "开盘前降低同主题暴露",
  "risk_notes": "若消息被市场快速消化，减仓可能错过反弹",
  "confidence": 0.66
}
```

`review_requests[]`：

```json
{
  "portfolio_id": "id-...",
  "symbol": "000977",
  "market": "SZ",
  "reason": "需要人工确认是否降低 AI 计算链条暴露",
  "priority": "high"
}
```

### 8.5 Prompt 要求

Prompt 必须要求 Agent：

- 区分 `facts`、`inferences`、`assumptions`。
- 标注利好、利空、不相关。
- 对每个高影响结论列出证据来源。
- 不确定时降低置信度，不生成操作提案。
- 不直接下单。
- 不声称已修改持仓。
- 有 proposed operation 时，只输出 proposal。
- 多持仓动作必须逐标的列出。
- 如果数据缺失或冲突，必须写入 `data_quality_notes`。

## 9. 派生对象落地

组合哨兵结果落地分三层。

### 9.1 只保存结果

当 `overall_risk_level=none` 或 Agent 输出无动作：

- 保存 `stockv2_portfolio_sentinel_results`。
- 不创建 `MonitorHit`。
- 不创建 `OperationReview`。

### 9.2 创建 Alert / MonitorHit

当有风险但不需要具体操作：

- 创建 synthetic `MonitorHit`，`task_type=portfolio_sentinel`。
- `symbol` 可为空或填主要受影响标的。
- `evidence` 写入 `sentinelRunId`、`sentinelResultId`、窗口、风险摘要、相关新闻。
- 创建 Alert，指向该 hit。

### 9.3 创建 OperationReview

当 Agent 输出 `portfolio_actions[]` 中包含 `proposed_operation`：

对每个 action：

```text
PortfolioSentinelResult
-> synthetic MonitorHit(symbol=单票)
-> CreateReviewFromMonitorHit
-> SaveOperationReviewResult(outputType=proposed_operation)
-> applyProposedOperationGuardrails
-> 等待用户 accept/reject/defer
```

注意：

- 每个 proposed operation 必须是单票。
- 组合层理由复制到每条 `MonitorHit.Evidence`。
- 如果 guardrails blocked，Review 仍保留，UI 显示 blocked 原因。
- 不自动 accept。

## 10. API

建议新增 HTTP API：

```text
GET  /api/stockv2/portfolio-sentinel/config
PUT  /api/stockv2/portfolio-sentinel/config
POST /api/stockv2/portfolio-sentinel/runs
GET  /api/stockv2/portfolio-sentinel/runs
GET  /api/stockv2/portfolio-sentinel/runs/{id}
GET  /api/stockv2/portfolio-sentinel/results/{id}
```

### 10.1 Config

```json
{
  "enabled": true,
  "preMarketEnabled": true,
  "middayEnabled": true,
  "postCloseEnabled": true,
  "maxNewsItems": 80,
  "maxNewsPerHolding": 20,
  "agentDoublecheckEnabled": true
}
```

第一版配置可以存 SQLite `stockv2_settings` 扩展字段，或新增单行配置表。为减少字段膨胀，建议新增 `stockv2_portfolio_sentinel_config`。

### 10.2 Run list filters

支持：

```text
status
windowType
triggerType
riskLevel
portfolioId
limit
offset
```

## 11. UI

StockV2 下新增二级入口：`组合哨兵`。

页面遵循 Quiet Agent Workbench 风格：

- 左侧/主区：运行历史列表。
- 顶部：启用状态、最近运行、手动触发按钮。
- 右侧 drawer/inspector：单次运行详情。
- 不使用营销 hero、大卡片、装饰图。

### 11.1 历史列表

列：

- 窗口类型。
- 时间范围。
- 状态。
- 风险级别。
- 扫描组合数 / 持仓数 / 新闻数。
- Agent 状态。
- 生成 Alert / Review 数。
- 完成时间。

### 11.2 详情视图

展示：

- Run summary。
- Context stats。
- Data freshness。
- Agent result summary。
- Positive / negative / noise items。
- Affected holdings。
- Generated Alerts。
- Generated OperationReviews。
- Guardrails result。
- DecisionLedger / AgentRun 链接。

### 11.3 手动触发

提供轻量弹窗：

- portfolio scope：默认全部当前组合。
- window type：manual。
- time range：默认最近 12 小时。
- note：可选。

## 12. 调度与幂等

### 12.1 调度

在 StockV2 background loop 中增加组合哨兵调度检查。

第一版用配置时间窗口 + 简单去重：

- 同一 `windowType + date + portfolioScope` 已完成则不重复跑。
- 手动触发不受该限制，但要防止同一 portfolio/window 同时 running。

### 12.2 幂等

必须防止重复生成 Review：

- `SentinelRun` 层：同一运行只 finalize 一次。
- 派生对象层：同一 `sentinelResultId + symbol + action` 生成唯一 `MonitorHit` 或在 evidence 中保存 dedupe key。
- Alert 层复用现有 dedupe 逻辑时，需要确保 dedupe key 包含 `portfolio_sentinel`、symbol、window、action。

## 13. 日志、脱敏与留痕

- 服务日志只记录运行失败、Agent 不可用、落库失败和派生对象失败摘要。
- 不记录完整 prompt、完整新闻正文、完整 Agent 原始输出到 service log。
- prompt / transcript 摘要进入 AgentRun / DecisionLedger。
- Sentinel result 的 `raw_result_json` 必须裁剪并走通用 redaction。
- URL 写入前移除 query 和 fragment。
- 不把 provider cookie、API key、FinancialJuice endpoint 参数写进文档、fixture 或日志。

## 14. 测试

### 14.1 Unit tests

- 无组合：创建 completed run，summary 为无扫描对象，不生成 Alert/Review。
- 有组合无持仓：同上。
- 有持仓无新闻：生成数据面摘要，不生成 proposed operation。
- 构造窗口新闻“美股存储概念股下跌，SNDK/WDC/MU 下跌” + 000977/601138/588180 持仓：Context 包含新闻和持仓。
- Agent 回填 `portfolio-sentinel-report/v1`：result 成功落库。
- Agent 回填多个 proposed operation：fan-out 成多条 OperationReview。
- guardrails blocked：Review 保存 blocked，不写 transaction。
- Agent unavailable：run failed 或 degraded，不生成伪结论。

### 14.2 Integration tests

- 手动触发 API 创建 run、AgentRun、DecisionLedger、SentinelResult。
- 历史列表 API 支持分页、过滤和详情。
- 周期调度不会重复创建同一窗口 run。
- 派生 Alert/Review 可从详情页查询到。

### 14.3 Regression tests

- 现有 `operation_review` Agent 继续可执行。
- 现有 `strategy_generation` 继续可执行。
- `data_strategy_monitor`、Watch 规则和操作 guardrails 不受影响。
- Review accept/reject/defer 和交易记录链路不受影响。

## 15. 开发顺序

建议按以下顺序开发，避免一次性改太大：

1. 数据模型与 repository：Sentinel run/result/config。
2. Context builder：无 Agent，仅可构造并测试窗口上下文。
3. Agent task profile / output validation / executor prompt。
4. 手动触发 API：先跑通 AgentRun + SentinelResult。
5. 派生对象：Alert / MonitorHit / OperationReview fan-out。
6. 历史 API。
7. UI 组合哨兵页面。
8. 后台调度。
9. 回归测试和真实库 smoke check。

## 16. Ponytail 最小实现判断

本能力确实需要存在，因为现有 monitor 不能覆盖窗口级组合风控：

- 不能只依赖消息关联候选：候选是召回材料，不负责窗口级组合判断。
- 不能只激活策略：`data_strategy_monitor` 依赖 active 策略，不覆盖无策略持仓。
- 不能只调组合阈值：激进组合本身允许高仓位，问题是信息面冲击下的临时风控。

最小正确实现不是大知识图谱，也不是全市场扫描，而是：

```text
当前持仓窗口上下文
-> 单次 Agent 判断
-> 独立历史
-> 必要时复用现有 OperationReview/guardrails
```

刻意延后：

- 全市场主题发现。
- 自动主题映射库。
- 删除 `NewsLinkCandidate`。
- 多 Agent 辩论。
- 自动交易执行。

## 17. 兼容性

- 不恢复 StockV1。
- 不修改旧 `stock_*` 残表。
- 不改变现有 `OperationReview` schema 的语义。
- 不要求已有策略必须 active。
- 空库或无持仓部署必须可用，输出无扫描对象。
- 缺 embedding 时允许关键词/名称召回降级，但必须在结果中标注语义召回不可用。
