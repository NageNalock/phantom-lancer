# StockV2 主板机会扫描设计

> 文档日期：2026-08-10
> 状态：已实现
> 关联设计：[StockV2 关键点](./stock-agent-workbench-v2-key-points-2026-06-18.md)、[主题机会发现](./stock-v2-opportunity-discovery-technical-design-2026-06-26.md)、[策略生成](./stock-v2-strategy-generation-design-2026-06-26.md)、[确定性决策门](./stock-v2-decision-gates-design-2026-08-14.md)

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
  → 股票画像补齐行业、概念和业务关键词
  → 行情排序 + 最近实质变化消息主题召回（合并后最多 200）
  → 前复权日线和最新报价补齐（最多 60）
  → 主备 Tushare 兼容源补齐主动资金证据（最多 30）
  → 基准、事件、财务事实与确定性四道门（研究池最多 20）
  → opportunity_discovery 有界证据复核（最多 20，输出最多 10）
  → strategy_generation 单次批处理（证据达标的全部最终候选，最多 10）
  → 未激活策略草案
```

本地预筛采用趋势、动量、量能、上涨成交占比、流动性、行业广度和 120 日相对位置的百分位组合。最终排序加入资金流、最新行情、消息脉络、数据质量和短期过热惩罚。分数用于缩小研究集合，不等同于投资结论。

全市场数据维护不再对每只股票逐个请求日线。系统先使用已配置的 Tushare 兼容主备源按 `trade_date` 分页拉取沪深北股票未复权日线，每页 5000 条，正常交易日约两次请求即可覆盖全市场；同一轮腾讯批量报价中的收盘 OHLCV 作为已完成交易日兜底。腾讯 fqkline 只用于本地不足 250 个交易日的历史补洞和 Agent 有界前复权补拉，不参与每天数千标的的逐股增量。盘中请求的结束日期固定在最近已完成日，仍在交易中的日线不会进入扫描覆盖率或指标计算。连续三次 HTTP 或网络失败会打开 15 分钟熔断，避免上游异常演变成重复请求、日志和磁盘写入风暴。

消息召回只读取最近 72 小时、置信度不低于 0.40、阶段为 emerging / spreading / accelerating / restarting 的实质变化主题。它依次使用明确股票代码或名称、股票画像结构字段、画像关键词和现有向量资产召回主板标的。最多 8 个消息候选可以越过行情前 200 门槛，最多 6 个保留 Agent 复核席位，总预算不扩张。纯语义召回不增加主题分，只获得研究席位并标记为“待因果核验”。

## 3. 固定资源边界

| 阶段 | 上限 |
| --- | ---: |
| 本地预筛 | 200 |
| 前复权与最新报价 | 60 |
| 120 日资金流 | 30 |
| Agent 证据复核 | 20 |
| 最终候选 | 10 |
| 策略草案 | 10 |
| 消息候选准入保留 | 8（包含在本地预筛 200 内） |
| 消息候选复核保留 | 6（包含在 Agent 复核 20 内） |

这些上限是个人服务器资源保护常量，不开放为运行时配置。前复权补拉按 200 毫秒最小间隔串行执行。主动资金证据使用固定白名单端点，DataHubCo 为主源、Indevs 为备源；资金流单源最多尝试两次，整个阶段最多占用 5 分钟。事件与财务参考数据在每个主备源内最多尝试三次，刷新失败时只复用 20 小时内同数据集缓存并将健康状态标记为降级。备源可使用仅作用于该 HTTP client 的独立代理，不修改进程全局代理。自动扫描要求主板标的最近交易日日线覆盖率至少 80%。

字段语义遵循 [Tushare moneyflow](https://tushare.pro/document/2?doc_id=170)，备源调用边界参考 [Indevs A 股资金流说明](https://ai-tool.indevs.in/quant/a-share-tick-level2-access/)。端点是实现常量，不向前端开放任意 URL，避免把数据源设置变成 SSRF 入口。

资金接口按 Tushare `moneyflow` 的 `net_mf_amount` 和四档买卖金额计算 5/20/60 日净流入、20 日主动资金比例和正流入天数。评分采用本轮截面百分位。30 个请求至少成功 24 个时才让资金维度参与整轮评分；覆盖不足时整轮忽略资金维度并重归一化其余权重，不填造中性 50 分。数据质量分只计算本轮适用维度，避免一次源级故障被重复惩罚。前复权、报价或资金源不可达不会丢弃本地预筛结果。

API Key 和备源代理仅保存在 SQLite 运行时配置，响应只返回 `configured` 布尔值。空输入保持原值，清除需要显式勾选。仓库、文档、日志、audit 和 Agent prompt 均不得出现凭据或带认证信息的代理 URL。

## 4. Agent 与策略生成

市场扫描复用现有 `opportunity_discovery` 任务绑定，不新增模型任务类型。上下文增加：

- `mode=market_scan`；
- `marketScanRunId`；
- 服务端预筛的最多 20 个 `marketCandidates`。
- 每个消息候选的 `threadId / versionId / matchKind / matchedTerms / semanticScore / requiresCausalVerification`。

模型只能从该集合返回候选，后端会再次验证子集关系和最多 10 个的输出上限。模型必须通过 `stock_agent.get_news_thread` 读取每个引用的准确主题版本；画像或语义命中不是受益证据，必须继续核验真实业务暴露、事件传导路径、价格是否已反映和失效条件。建议绑定 `GPT-5.6-Terra / Codex CLI / medium`，但实现不会覆盖 owner 已保存的模型、执行模式或留空的推理强度。

证据分数至少 55、置信度至少 0.55 的最终候选进入一次批量 `strategy_generation`，最多 10 个。进入策略生成前只刷新这些标的的最新报价并持久化，保证 Agent 初始上下文和 MCP 查询读取同一份带时间戳的未复权执行价格。机会扫描生成的是研究范围策略，`portfolioId` 可以按设计留空；缺少组合上下文不能被当成拒绝生成研究草案的理由，仓位大小留待后续绑定组合和人工 Review。生成结果保持 `draft`，规则应能被现有数据面策略监控消费。候选会从 `strategy_requested` 闭环到 `strategy_generated` 并保存策略 ID；如果全部候选都没有形成可监控方案，允许以零草案正常完成并保留逐候选的未入选理由。

## 5. 调度、恢复与失败

- 自动扫描默认关闭，在前端“机会发现 → 市场扫描 → 配置”中启用。
- 每次成功的全市场数据维护后检查一次；同一新交易日最多创建一个自动 run。
- 全市场数据维护成功率低于 80% 时任务状态为 `failed`，不会以带大量条目失败的 `completed` 状态放行市场扫描。夜间自动维护失败后间隔 15 分钟重试，单个夜间窗口最多三次；耗尽预算后保留最终失败记录，等待下一窗口或人工处理。
- 当日扫描抓取主题快照后又出现重要实质变化主题时，复用当日市场数据最多创建一个 `theme_refresh` 补扫 run；补扫失败使用正常重试预算，不重复创建第二个补扫。
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
- `stockv2_decision_gate_snapshots` / `stockv2_decision_gate_outcomes`
- `stockv2_decision_market_events` / `stockv2_decision_financial_facts`
- `stockv2_decision_index_bars`

系统复用一个 `created_by=system:market_scan` 的 Opportunity，避免每日制造孤立机会对象。

HTTP API：

- `GET/PATCH /api/stockv2/opportunity-market-scan/config`
- `POST /api/stockv2/opportunity-market-scan/fund-flow/probe`
- `POST /api/stockv2/opportunity-market-scan/decision-data/probe`
- `GET/POST /api/stockv2/opportunity-market-scan/runs`
- `GET /api/stockv2/opportunity-market-scan/runs/{id}`
- `GET /api/stockv2/opportunity-market-scan/runs/{id}/candidates`
- `POST /api/stockv2/opportunity-market-scan/runs/{id}/retry`

写操作要求认证、CSRF 并写入 audit。服务日志只记录边界失败摘要，不记录 prompt、外部响应正文或密钥。

## 7. 前端

StockV2 一级入口改名为“机会发现”，二级视图为：

- 市场扫描：覆盖率、当前阶段、运行历史、候选表、候选详情、自动扫描配置和失败重试。覆盖不足或最近一次全市场数据维护失败时，首屏状态区直接展示维护进度、成功/失败计数和脱敏错误摘要。
- 主题研究：保留手工主题、研究步骤和候选池，只展示紧凑的语义召回可用性与管理入口。完整的模型绑定、向量维护和资产明细归入“Agent → 语义召回”。

运行中的扫描只展示“当前研究池”。终态扫描按“最终入选 / 复核未入选 / 预筛排除”分组，默认展示最终入选；排除项每页 50 条。历史终态中遗留的 `research_candidate` 会幂等迁移为 `reviewed_out`，避免把已经结束的复核显示为“待复核”。

候选列表以单行省略形式展示筛选结论、四道门通过数、数据健康和“行情预筛 / 行情+消息 / 消息驱动”来源，并支持过滤消息相关候选。详情抽屉展示主题版本、匹配方式、命中词、语义分数和待因果核验状态。固定 5 日 18% / 20 日 35% 边界已由 ATR 动态波动门替代；详情同时展示催化定价、因子拥挤、事件保护、来源健康与 1/3/5/10/20 交易日事后验证。历史记录从已保存的发现报告与策略决策账本按需恢复，不重新运行模型，也不伪装成已经通过新门。

界面统一使用“主动资金证据”，并明确它与账户可用资金无关。候选会区分“可用并采用、排名预算外未请求、源不可用或数据无效、覆盖不足导致本轮未采用”，不再笼统显示“资金缺失”。固定预算、模型建议和低频数据源设置进入配置抽屉，保持 Quiet Agent Workbench 的低噪音信息层级。
