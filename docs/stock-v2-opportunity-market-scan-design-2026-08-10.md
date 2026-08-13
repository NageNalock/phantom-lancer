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
  → 主备 Tushare 兼容源补齐主动资金证据（最多 30）
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

这些上限是个人服务器资源保护常量，不开放为运行时配置。前复权补拉按 200 毫秒最小间隔串行执行。主动资金证据使用固定白名单端点，DataHubCo 为主源、Indevs 为备源；单源最多尝试两次，整个阶段最多占用 5 分钟。备源可使用仅作用于该 HTTP client 的独立代理，不修改进程全局代理。自动扫描要求主板标的最近交易日日线覆盖率至少 80%。

字段语义遵循 [Tushare moneyflow](https://tushare.pro/document/2?doc_id=170)，备源调用边界参考 [Indevs A 股资金流说明](https://ai-tool.indevs.in/quant/a-share-tick-level2-access/)。端点是实现常量，不向前端开放任意 URL，避免把数据源设置变成 SSRF 入口。

资金接口按 Tushare `moneyflow` 的 `net_mf_amount` 和四档买卖金额计算 5/20/60 日净流入、20 日主动资金比例和正流入天数。评分采用本轮截面百分位。30 个请求至少成功 24 个时才让资金维度参与整轮评分；覆盖不足时整轮忽略资金维度并重归一化其余权重，不填造中性 50 分。数据质量分只计算本轮适用维度，避免一次源级故障被重复惩罚。前复权、报价或资金源不可达不会丢弃本地预筛结果。

API Key 和备源代理仅保存在 SQLite 运行时配置，响应只返回 `configured` 布尔值。空输入保持原值，清除需要显式勾选。仓库、文档、日志、audit 和 Agent prompt 均不得出现凭据或带认证信息的代理 URL。

## 4. Agent 与策略生成

市场扫描复用现有 `opportunity_discovery` 任务绑定，不新增模型任务类型。上下文增加：

- `mode=market_scan`；
- `marketScanRunId`；
- 服务端预筛的最多 20 个 `marketCandidates`。

模型只能从该集合返回候选，后端会再次验证子集关系和最多 10 个的输出上限。建议绑定 `GPT-5.6-Terra / Codex CLI / medium`，但实现不会覆盖 owner 已保存的模型、执行模式或留空的推理强度。

证据分数至少 55、置信度至少 0.55 的前三名进入一次批量 `strategy_generation`。进入策略生成前只刷新这最多三只标的的最新报价并持久化，保证 Agent 初始上下文和 MCP 查询读取同一份带时间戳的未复权执行价格。机会扫描生成的是研究范围策略，`portfolioId` 可以按设计留空；缺少组合上下文不能被当成拒绝生成研究草案的理由，仓位大小留待后续绑定组合和人工 Review。生成结果保持 `draft`，规则应能被现有数据面策略监控消费。候选会从 `strategy_requested` 闭环到 `strategy_generated` 并保存策略 ID；如果全部候选都没有形成可监控方案，允许以零草案正常完成并保留逐候选的未入选理由。

## 5. 调度、恢复与失败

- 自动扫描默认关闭，在前端“机会发现 → 市场扫描 → 配置”中启用。
- 每次成功的全市场数据维护后检查一次；同一新交易日最多创建一个自动 run。
- 手动扫描不依赖自动开关，但仍要求数据覆盖率达标且当前没有其他扫描。
- 运行阶段为 `pending / prefiltering / enriching / researching / drafting / completed / partial / failed`。
- 模型复核和策略生成失败分别在 5 分钟、30 分钟后重试，最多两次；服务重启后由持久状态继续推进。
- 策略生成若仅因已知结构别名校验失败，会在校验器升级后复用决策账本中的原始结果重新校验和落草案，不重新请求模型；Provider、超时、无结果等执行失败仍走正常重试预算。
- 策略生成最终失败时保留已经完成的候选研究并标记 `partial`；所有失败摘要在前端可见且可手动重试。

## 6. 数据与 API

SQLite 表：

- `stockv2_opportunity_market_scan_config`
- `stockv2_opportunity_market_scan_runs`
- `stockv2_opportunity_market_scan_candidates`

系统复用一个 `created_by=system:market_scan` 的 Opportunity，避免每日制造孤立机会对象。

HTTP API：

- `GET/PATCH /api/stockv2/opportunity-market-scan/config`
- `POST /api/stockv2/opportunity-market-scan/fund-flow/probe`
- `GET/POST /api/stockv2/opportunity-market-scan/runs`
- `GET /api/stockv2/opportunity-market-scan/runs/{id}`
- `GET /api/stockv2/opportunity-market-scan/runs/{id}/candidates`
- `POST /api/stockv2/opportunity-market-scan/runs/{id}/retry`

写操作要求认证、CSRF 并写入 audit。服务日志只记录边界失败摘要，不记录 prompt、外部响应正文或密钥。

## 7. 前端

StockV2 一级入口改名为“机会发现”，二级视图为：

- 市场扫描：覆盖率、当前阶段、运行历史、候选表、候选详情、自动扫描配置和失败重试。
- 主题研究：保留手工主题、研究步骤和候选池，只展示紧凑的语义召回可用性与管理入口。完整的模型绑定、向量维护和资产明细归入“Agent → 语义召回”。

运行中的扫描只展示“当前研究池”。终态扫描按“最终入选 / 复核未入选 / 预筛排除”分组，默认展示最终入选；排除项每页 50 条。历史终态中遗留的 `research_candidate` 会幂等迁移为 `reviewed_out`，避免把已经结束的复核显示为“待复核”。

界面统一使用“主动资金证据”，并明确它与账户可用资金无关。候选会区分“可用并采用、排名预算外未请求、源不可用或数据无效、覆盖不足导致本轮未采用”，不再笼统显示“资金缺失”。固定预算、模型建议和低频数据源设置进入配置抽屉，保持 Quiet Agent Workbench 的低噪音信息层级。
