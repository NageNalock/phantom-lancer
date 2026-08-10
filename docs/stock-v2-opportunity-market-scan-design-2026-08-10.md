# StockV2 主板机会扫描设计

> 文档日期：2026-08-10
> 状态：已实现
> 关联设计：[StockV2 关键点](./stock-agent-workbench-v2-key-points-2026-06-18.md)、[主题机会发现](./stock-v2-opportunity-discovery-technical-design-2026-06-26.md)、[策略生成](./stock-v2-strategy-generation-design-2026-06-26.md)

## 1. 目标与边界

“市场扫描”解决现有系统只围绕已持仓和 owner 已知主题运行、不能主动发现新标的的问题。它是 StockV2“机会发现”下的一个子视图，与原有“主题研究”并列；不修改组合哨兵和消息脉络的职责。

首版只扫描沪深主板普通股票：

- 上海 `600/601/603/605`；深圳 `000/001/002/003`。
- 排除科创板、创业板、北交所、ETF、ST、退市整理以及名称以 N/C 开头的新股。
- 只生成研究候选和未激活策略草案，不买卖、不改持仓、不自动激活策略。

## 2. 执行链路

```text
全市场数据维护成功
  → DuckDB 一次性读取未复权行情和主数据
  → 确定性主板过滤与排序（最多 200）
  → 前复权日线和最新报价补齐（最多 60）
  → 资金流串行补齐（最多 30）
  → 活跃消息脉络匹配
  → opportunity_discovery 有界证据复核（最多 20，输出最多 10）
  → strategy_generation 单次批处理（证据达标的前 3）
  → 未激活策略草案
```

本地预筛采用趋势、动量、量能、上涨成交占比、流动性、行业广度和 120 日相对位置的百分位组合。最终排序加入资金流、最新行情、消息脉络、数据质量和短期过热惩罚。分数用于缩小研究集合，不等同于投资结论。

## 3. 固定资源边界

| 阶段 | 上限 |
| --- | ---: |
| 本地预筛 | 200 |
| 前复权与最新报价 | 60 |
| 120 日资金流 | 30 |
| Agent 证据复核 | 20 |
| 最终候选 | 10 |
| 策略草案 | 3 |

这些上限是个人服务器资源保护常量，不开放为运行时配置。前复权补拉按 200 毫秒最小间隔串行执行；资金流请求串行执行，最小间隔 1.25 秒并加入小幅固定抖动。自动扫描要求主板标的最近交易日日线覆盖率至少 80%。

外部前复权、报价或资金流源不可达时，任务保留候选但把对应数据质量标为缺失；资金流采用中性分而不是把缺失解释成流入或流出。单个可选数据源失败不会丢弃整轮本地预筛结果，候选详情会显示实际覆盖情况。

## 4. Agent 与策略生成

市场扫描复用现有 `opportunity_discovery` 任务绑定，不新增模型任务类型。上下文增加：

- `mode=market_scan`；
- `marketScanRunId`；
- 服务端预筛的最多 20 个 `marketCandidates`。

模型只能从该集合返回候选，后端会再次验证子集关系和最多 10 个的输出上限。建议绑定 `GPT-5.6-Terra / Codex CLI / medium`，但实现不会覆盖 owner 已保存的模型、执行模式或留空的推理强度。

证据分数至少 55、置信度至少 0.55 的前三名进入一次批量 `strategy_generation`。生成结果保持 `draft`，规则应能被现有数据面策略监控消费。候选会从 `strategy_requested` 闭环到 `strategy_generated` 并保存策略 ID。

## 5. 调度、恢复与失败

- 自动扫描默认关闭，在前端“机会发现 → 市场扫描 → 配置”中启用。
- 每次成功的全市场数据维护后检查一次；同一新交易日最多创建一个自动 run。
- 手动扫描不依赖自动开关，但仍要求数据覆盖率达标且当前没有其他扫描。
- 运行阶段为 `pending / prefiltering / enriching / researching / drafting / completed / partial / failed`。
- 模型复核和策略生成失败分别在 5 分钟、30 分钟后重试，最多两次；服务重启后由持久状态继续推进。
- 策略生成最终失败时保留已经完成的候选研究并标记 `partial`；所有失败摘要在前端可见且可手动重试。

## 6. 数据与 API

SQLite 表：

- `stockv2_opportunity_market_scan_config`
- `stockv2_opportunity_market_scan_runs`
- `stockv2_opportunity_market_scan_candidates`

系统复用一个 `created_by=system:market_scan` 的 Opportunity，避免每日制造孤立机会对象。

HTTP API：

- `GET/PATCH /api/stockv2/opportunity-market-scan/config`
- `GET/POST /api/stockv2/opportunity-market-scan/runs`
- `GET /api/stockv2/opportunity-market-scan/runs/{id}`
- `GET /api/stockv2/opportunity-market-scan/runs/{id}/candidates`
- `POST /api/stockv2/opportunity-market-scan/runs/{id}/retry`

写操作要求认证、CSRF 并写入 audit。服务日志只记录边界失败摘要，不记录 prompt、外部响应正文或密钥。

## 7. 前端

StockV2 一级入口改名为“机会发现”，二级视图为：

- 市场扫描：覆盖率、当前阶段、运行历史、候选表、候选详情、自动扫描配置和失败重试。
- 主题研究：保留原有手工主题、向量状态、研究步骤和候选池。

主界面只常驻当前状态与候选，固定预算、模型建议和低频设置进入配置抽屉，保持 Quiet Agent Workbench 的低噪音信息层级。
