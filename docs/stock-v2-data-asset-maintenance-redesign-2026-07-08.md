# 股票 V2 数据资产维护重构设计

> 文档日期：2026-07-08
>
> 状态：V2 数据资产维护重构已实现，按 2026-07-12 的本机资源约束验收
>
> REPLACES：当前“统一数据资产维护 + 独立画像自动维护 / 深度画像队列”的并行实现思路。后续实现应以本文为准，将画像基础更新、公告/重大事项增量和 AI 画像总结并入统一数据资产维护管线。
>
> 相关文档：`docs/stock-agent-workbench-v2-key-points-2026-06-18.md` 定义 StockV2 数据资产层与信息面链路；`docs/stock-v2-strategy-generation-design-2026-06-26.md` 和 `docs/stock-v2-opportunity-discovery-technical-design-2026-06-26.md` 依赖稳定的股票画像、行情和消息资产。

## 1. 背景

当前 StockV2 的数据资产维护分成多条弱关联链路：

- 统一维护任务维护标的主数据和日 K。
- 独立画像维护任务全量重建基础画像，再用小批量“深度画像队列”更新少数标的。
- 消息面已有新闻事件链路，但没有正式公告 / 重大事项持久化资产。

这导致几个问题：

- 单只持仓可能长期只有很薄的基础画像，例如只有代码、名称和市场。
- “深度画像队列”按全市场滚动处理，不能保证持仓、活跃策略或有重大信息变化的标的优先。
- AI 画像总结只被基础画像 hash 间接触发，不能响应公告 / 重大事项。
- 前端把数据资产维护、日 K、画像配置和画像记录拆成多个入口，用户很难判断真实依赖关系。
- `stockv2_instruments.status=active` 当前不是真实上市 / 停牌 / 退市状态，只是实现中写入的默认状态，不应作为核心调度依据。

## 2. 目标

数据资产维护应成为 StockV2 的统一资产管线，覆盖数据面和信息面：

```text
选择待维护标的
-> 补齐日 K 缺口
-> 刷新基础画像输入
-> 检查公告 / 重大事项增量
-> 判定是否需要 AI 画像总结
-> 触发 AI 画像总结
-> 标记向量资产 stale
-> 记录任务明细与前端可观测状态
```

目标行为：

1. 如果缺少日 K，只补缺失区间，不做无意义全量覆盖。
2. 日 K 应逐步承载更完整的数据面字段，包括换手率、成交额、资金流、买入 / 卖出等可得信息。
3. 每次维护检查基础画像输入是否变化，变化时同步基础画像。
4. 每次维护检查是否有最新公告、重大事项披露或其他强信息面变化。
5. 基础画像变化、新公告 / 重大事项、AI 画像缺失或上次失败可重试时，触发 AI 画像总结。
6. AI 画像总结上下文必须包含上次 AI 总结、基础画像 diff、新公告 / 重大事项和必要数据面摘要。
7. 任务需要限速、分批、失败退避和可恢复，避免对本机、DuckDB、外部数据源和 Agent 模型造成过大压力。

## 3. 非目标

本重构不负责：

- 自动生成交易操作。
- 自动修改持仓。
- 用 Agent 替代确定性数据采集。
- 把外部网页搜索能力做进主程序。外部研究仍由 Codex CLI 在 Agent 任务中按需完成。
- 一次性建成公告 OCR、版面 / 表格结构化抽取、完整全文语义解析、财报解析和产业知识图谱。当前只提供受限的 PDF 文本摘录，不把扫描件或复杂版式描述成已完成全文理解。

第一版应先做稳定的增量资产维护和触发机制，再扩展更复杂的信息抽取。

## 4. 废弃旧画像维护模型

旧“画像配置”和“深度画像队列”不再作为独立产品能力保留。

废弃内容：

- 独立的“自动画像更新”开关。
- 独立的“每轮标的数 / 每标的 AI 轮数 / 单只间隔”画像配置。
- 按全市场散列滚动的小批量深度画像队列。
- 先全量重建基础画像、再另跑深度画像的两段式用户心智。

替代方案：

- 统一数据资产维护任务负责基础画像刷新、公告检查和 AI 画像触发。
- AI 调用预算、并发、限速和优先级放进统一维护配置。
- 画像更新记录仍保留，但作为统一维护的 per-symbol 明细视图，不再是独立任务系统。
- 单标的手动“更新画像”可以继续存在，但内部调用同一条 per-symbol 资产维护管线，并允许只处理该 symbol。

迁移要求：
- 旧的设置信息可以全部放弃, 不要默兼容, 导致新的逻辑不符合预期

## 5. 维护对象范围

统一维护按优先级选择标的，不再依赖伪 `active instruments` 概念。

优先级：

1. 当前持仓。
2. 活跃策略关联标的。
3. 近期 MonitorHit / Alert / Review 关联标的。
4. 近期有公告、重大事项或高置信消息候选的标的。
5. 基础画像缺失、AI 画像缺失或 AI 画像失败的标的。
6. 日 K 缺失、日 K 不足或数据面字段缺失的标的。
7. 其他标的低频轮转维护。

全市场标的池仍来自公开行情列表，但它只是“可维护候选池”，不是“active 可交易状态”。真实上市、停牌、退市、可交易状态应未来用明确数据源维护，不复用当前 `status=active`。

## 6. 数据模型

### 6.1 stockv2_asset_maintenance_items

新增统一维护明细表，记录每只标的在一次维护任务中的处理结果。

字段建议：

- `id`
- `job_id`
- `symbol`
- `market`
- `instrument_type`
- `priority_reason`
- `status`
- `daily_bar_status`
- `daily_bar_from`
- `daily_bar_to`
- `daily_bar_inserted_count`
- `daily_bar_gap_count`
- `profile_base_status`
- `profile_base_changed`
- `profile_base_hash_before`
- `profile_base_hash_after`
- `announcement_status`
- `announcement_new_count`
- `major_event_new_count`
- `ai_decision`
- `agent_run_id`
- `embedding_marked_stale`
- `error_message`
- `started_at`
- `finished_at`
- `created_at`
- `updated_at`

`status` 表示该 symbol 维护结果：`pending`、`running`、`completed`、`partial`、`failed`、`skipped`。

`ai_decision` 建议值：

- `called`
- `skipped_unchanged`
- `skipped_not_configured`
- `skipped_unavailable`
- `failed`

### 6.2 stockv2_announcements

新增正式公告 / 重大事项表，不混入普通新闻事件。

字段建议：

- `id`
- `symbol`
- `market`
- `title`
- `category`
- `published_at`
- `source`
- `source_url`
- `doc_url`
- `content_hash`
- `importance`
- `is_major_event`
- `summary`
- `raw_payload_json`
- `created_at`
- `updated_at`

去重规则：

- 首选：`source + symbol + content_hash`。
- 兜底：`source + symbol + title + published_at`。

重大事项第一版可用标题和分类规则判断，包括但不限于：

- 重大合同 / 中标。
- 资产重组 / 并购。
- 业绩预告 / 业绩快报。
- 定增 / 回购 / 股权激励。
- 控股股东或高管增减持。
- 诉讼 / 仲裁 / 监管处罚。
- 对外担保 / 重大投资。
- 停复牌 / 风险警示。
- 问询函 / 监管函 / 回复公告。

### 6.3 日 K 扩展字段

当前日 K 表已在 OHLCV、前收、成交额和涨跌幅之外持久化以下数据面字段：

稳定字段：

- `turnover_rate`
- `net_inflow`
- `main_net_inflow`

每个字段同时保存 presence，不能把上游 `-`、`--` 或空值伪装为合法的 `0`。质量汇总通过
`incomplete_count` / `facets_complete` 暴露数据面缺失；股票要求成交额、换手率和资金流，
场内基金只要求其数据源可稳定提供的成交额和换手率。

注：`buy_amount` / `sell_amount` 已移除（无可靠数据源替代）。当前保存的是主力及分档净流入，
不能将其描述为逐笔买入额 / 卖出额。

来源差异较大的字段先进入：

- `data_payload_json`

这样既能支持前端和 Agent 使用关键字段，又避免数据源变化时频繁迁表。

## 7. 日 K 缺口维护

实现同时检查日期覆盖、字段 presence、`row_count`、`latest_date` 和 stale 状态，可以识别中间缺口及部分字段行。

目标逻辑：

1. 确定目标维护窗口。自动维护使用 420 个自然日，稳定覆盖至少 250 个交易日；手动范围保持原有语义。
2. 查询本地已有交易日期集合。
3. 根据交易日历或数据源返回日期判断缺口。
4. 将缺失日期合并成连续区间。
5. 只抓取缺失区间。
6. 写入 DuckDB 时使用 upsert，重复抓取不破坏旧数据。
7. 记录每个 symbol 的缺口数量、抓取区间和写入条数。

当前支持尾部缺口和交易日历确认的中间缺口：

- 完全没有数据：抓取目标窗口。
- 最近日期落后：从本地最近日期下一天抓到今天。
- 中间缺口：按缺口段抓取。

每轮批量维护额外只请求一次腾讯上证指数 K 线作为独立交易日历锚，并持久化日期；失败时回退到本地已观测日历，
不会为每个标的重复请求日历。权威日历失败时不再中止独立的 F10 / 公告检查，但本轮 market freshness 保持
`retrying`；失败尝试和 15 分钟共享退避持久化，由低成本日历重试器恢复，不能用普通日 K 观测洗成权威 ready。

### 7.1 数据源

日 K 数据源按字段完整性降级：

1. 同花顺 line（`d.10jqka.com.cn/v6/line`）作为不复权主源，一次返回目标窗口所需的 OHLCV、`amount`（成交额）和 `turnover_rate`（换手率）。
2. 腾讯 fqkline 作为第二源；它不含 amount/turnover_rate，因此先保留为 OHLCV 部分兜底，同时继续尝试最终字段完整兜底。
3. 百度财经（`finance.pae.baidu.com/selfselect/getstockquotation`）仅在前两源仍未完整覆盖目标交易日时尝试；百度仍失败时保留已成功的同花顺 / 腾讯日期，不因补充字段失败丢弃行情。

当前交易日先使用批量收盘行情（每批最多 80 个标的），东财提供成交额、换手率和分档净流入，失败时整批降级腾讯。整个维护任务只用首批探测一次 `QuoteAt` 众数以确定真实交易日；法定休市时复用本地最近交易日，不会对全市场发无效请求，也不会退化为逐股请求。

历史缺口继续按同花顺 → 腾讯 → 百度顺序逐标的补齐。同一标的存在多个缺口时，以最早开始日和最晚结束日组成一个请求区间，响应在本地过滤，并按交易日择优合并多个源的互补结果，避免按缺口逐段请求或被某个源的部分响应提前截断。请求窗口按上市日/退市日裁剪；T-3 以前的空档必须至少两个独立源都成功返回空结果才写入持久化 negative coverage，且 30 天后重新验证，避免供应商漏行被永久缓存。包络内不属于本次缺口的日期不能扩大成无成交证据；最近三天始终允许延迟修正。每次成功落盘或指数锚确认的交易日会进入 `stockv2_trading_calendar`，中间缺口不把春节、国庆等休市日期当成缺口。百度仅是最终兜底，403 会进入冷却，不中断其他标的的基础维护。

- 东财资金流端点（`push2his.eastmoney.com/api/qt/stock/fflow/daykline/get`）仅在股票行缺少资金流字段时提供 `NetInflow`/`MainNetInflow` enrichment；只缺资金流时直接修复本地行，不重拉历史 K。首个失败触发 10 分钟共享冷却，避免外部故障放大为全市场逐股请求。

## 8. 基础画像维护

基础画像输入应包括：

- 标的代码、市场、类型、名称。
- 行业、板块、概念。
- 主营业务、经营范围、主营构成。
- 公司简介。
- 必要的近期数据面摘要，例如成交量、换手率、资金流特征。
- 必要的已确认消息主题摘要。
- 人工补充信息。

每次维护生成基础画像输入 hash：

```text
base_profile_hash = hash(normalized_base_profile_input)
```

如果 hash 变化：

- 更新基础画像字段。
- 标记 `profile_base_changed=true`。
- 标记对应股票画像 embedding stale。
- 进入 AI 画像触发判断。

如果 hash 不变：

- 不重复写大量字段。
- 不触发 AI，除非存在新公告 / 重大事项或 AI 画像缺失。

### 8.1 F10 刷新缓存

批量维护中，基础画像的 F10 抓取（东财 3 个端点）成本较高但变化频率低。
因此内置 7 天缓存：如果 `base_profile_checked_at`（旧数据回退 `base_profile_updated_at`）在 7 天内，跳过 F10 抓取，
每天仍用批量行情和 instrument 做名称/市场/类型及本地基础字段比较；发现变化才提前绕过缓存。
`base_profile_updated_at` 只表示基础内容版本变化时间，`base_profile_checked_at` 表示最近一次完整 F10 成功检查时间；
内容未变只推进 checked，AI 新鲜度仍与 updated 内容版本比较。完整 F10 刷新失败时两个时间都不推进。
首个网络超时、403、429 或 5xx 会触发 10 分钟 provider 级冷却，避免故障时继续执行“全市场标的数 × 3”请求。

- 节省：每只股票 3 次 HTTP 请求（日常维护中大部分股票命中缓存）
- 代价：F10 级别变化（如经营范围变更）最多延迟 7 天检测
- 手动触发（ForceAI）时绕过缓存，立即刷新

## 9. 公告和重大事项维护

公告检查应成为统一维护的一部分。

批量维护时：

1. 按 SH / SZ / BJ 三个市场读取持久化 `covered_through`。
2. 使用 6 小时重叠窗口分页拉取巨潮全市场公告，不再逐股请求。
3. CNINFO HTTP 200 仍必须校验响应 envelope：`announcements` 必须是数组，并且至少存在一个合法的总数字段；空响应、`null`、未知 shape 或字段漂移全部 fail-closed。
4. 所有市场、所有分页以及当日历史复核桶成功后，一次事务完成精确去重、公告落库和 cursor 推进。
5. 任一分页或 envelope 校验失败时不落部分批次、不推进任何 cursor，且不退化为数千次单股请求。
6. 除 6 小时增量窗口外，每个市场每个上海自然日最多复核一个历史日期桶。`late_recheck_started_at`、`late_recheck_covered_through` 和 `last_late_recheck_at` 持久化该进度；手动维护和失败重试不能在同一天连续推进多个桶。
7. 新公告在内存中按 symbol 分发；本地一次批量查询补充“上次 AI 之后”的公告上下文。
8. 标记重大事项并统计 `announcement_new_count` / `major_event_new_count`，再进入 AI 触发判断。

历史复核从首次启用日的 D-30 桶开始，每个成功的上海自然日向前推进一个日期桶。首次严格 warm-up 约需 30 个成功维护日；完成前 `message_ready` / `analysis_ready` 保持 false，并返回 message reason `announcement_late_recheck_incomplete`，只有调用方显式选择 degraded 模式才能继续。warm-up 后维持约 30 天的迟到发现上界：已入库公告仍同时保留 `published_at` 和首次 `fetched_at`，所以旧发布时间的迟到记录也会触发新版 AI；但第三方持续失败或公告迟到超过 30 天时不能承诺发现。每标的最近上下文上限为 100 条，不再因旧的 20 条限制漏掉常见积压。

单股手动维护仍保留单股巨潮查询路径。

公告资产与普通新闻资产的关系：

- 公告是强信息面资产，来源明确、可追溯、可长期保存。
- 新闻事件是消息面候选资产，用于高召回监控和主题发现。
- 两者可以共同进入 Agent Context Pack，但不应混在同一张表里。

当前版本持久化官方公告 ID、标题、分类、发布时间、重大事项规则、版本号和官方 PDF URL，并以这些强来源元数据触发 AI 重建。重大公告正文由可选的 Poppler `pdftotext` worker 处理：只允许巨潮官方 HTTPS PDF，单 PDF 上限 8 MiB，只抽取前 8 页并保存最多 24 KiB 摘录；worker 每 5 分钟运行一次、每批最多 4 份，上海自然日预算为 20 次请求和 64 MiB，claim、lease、重试和预算均持久化。

部署机找不到 `pdftotext` 时，worker 不 claim、也不发起 PDF 网络请求，公告保持 `metadata_only`，readiness overview 通过 `announcementBodyParserAvailable=false` 明确暴露部署缺口。存在正文尚未达到 `text_ready` 的重大公告时，严格 analysis readiness 必须阻止消费，返回 limitation `major_announcement_content_status_unavailable` 和 analysis reason `major_announcement_content_unavailable`；显式 degraded 模式可以继续，但必须展示该限制。扫描 PDF 的 OCR、复杂版面 / 表格结构化抽取和完整全文语义解析仍是非目标，空文本或仅扫描件不能被洗成 ready。

## 10. AI 画像总结触发

统一维护中每个 symbol 独立判断是否触发 AI。

触发条件：

1. `ai_profile_status` 为空或 `missing`。
2. 基础画像 hash 变化。
3. 新增公告或重大事项。
4. 上次 AI 失败，且本次输入可用并满足重试退避。
5. 用户手动强制更新。

跳过条件：

1. 基础画像无变化。
2. 没有新增公告 / 重大事项。
3. 已有 AI 画像总结。
4. 模型未配置或执行器不可用。

AI prompt 必须包含：

- 当前基础画像。
- 上次 AI 画像总结。
- 本次基础画像 diff。
- 新公告 / 重大事项摘要。
- 日 K / 换手率 / 资金流等数据面摘要。
- 来源状态和数据质量提示。

AI 输出仍只更新画像总结和检索辅助字段，不生成交易建议。

AI 任务写入 `stockv2_stock_profile_ai_queue`：同一 symbol 只有一行，输入版本相同则合并，运行中出现新输入则保留新版本并在旧运行结束后重新排队。SQLite 队列只保存 symbol、目标版本、优先级和 lease 等引用状态，不保存可陈旧的序列化 prompt/payload；worker 每次从 DuckDB 权威状态重建最新上下文。worker 使用 lease、心跳和退避，服务重启时恢复未完成任务；旧版本结果在写画像前再次校验版本，不能覆盖新输入。公告上下文失败时队列进入 `retry_wait`，不会使用不完整上下文生成；后续公告同步成功会按最新权威版本重新协调入队。上一版 AI 总结从不可变的已应用版本读取并位于 prompt 前部，整体截断时仍会保留。

权威目标版本为 `base_profile_hash + announcement_revision + data_summary_version + manual_generation` 的稳定摘要。最近 5 根日 K 的数据摘要只会刷新已经 pending / running 的目标，防止执行期间行情变化让旧结果落库；一个已 ready 且基础画像、公告均未变化的标的不会仅因每日行情滚动而每天重跑 AI。应用新版本前会移除上一不可变版本贡献的 AI 标签，再合并新结果，同时保留 F10 与 AI 同名的基础字段，避免画像标签永久累积或误删基础数据。

`ai_profile_updated_at` 只表示最后一次成功画像时间，`ai_profile_attempted_at` 单独记录失败尝试并用于退避。因此失败重试仍会携带“最后成功画像之后”的全部公告。基础画像合并与 AI 结果落库按 symbol 串行，避免异步 worker 与基础刷新互相覆盖字段。

## 11. 资源与速度控制

统一维护会比当前任务更重，必须内置资源控制。

### 11.1 调度预算

建议配置：

- 默认维护本地和已发现 universe 的全部 symbol，不再隐式截断为 5000。
- 显式设置每轮最大 symbol 数时，先放入持仓 / 活跃策略标的，再对剩余容量分配缺失/过期 priority cursor 和全 universe cursor；通常为全 universe 保留至少 25% 总容量，但安全优先标的已经占满显式上限时除外。尚未写入 instrument 的已发现标的也参与轮转。
- 每轮最大持仓 / 策略高优先级 symbol 数。
- 每轮最大公告检查数。
- 每轮最大日 K 缺口抓取数。
- 每 symbol 最大耗时。
- 整个任务最大耗时。

注：AI 调用不设数量上限，由触发条件（基础画像变化、新公告、AI 缺失、失败重试）自然控制。

### 11.2 并发限制

实现配置：

- 标的维护管线并发：本机资源正常时 2 worker，受限时降为 1 worker；命中本地/F10 缓存的标的不再做固定逐股 sleep。
- 当前交易日：真实交易日单次 probe + 80 标的一批的东财/腾讯行情；东财首个批次失败后进入 10 分钟冷却，其余批次直接走腾讯。批量结果只落盘一次，后续逐标的管线不重复写。
- 历史日 K：同花顺单请求主源；失败后腾讯提供 OHLCV 部分兜底，百度只负责最终字段完整兜底。稳定空档持久化，最近三天可重试。
- 成功的全量 universe 发现结果按 generation 持久缓存 24 小时；任一市场节点或分页失败都不能提交为完整 generation。失败时保留已有完整缓存、将当前任务标记为 universe unverified，并在 6 小时后重试，不会把部分结果或内置样本误当完整市场。日常任务直接读本地主数据，仅对新发现、尚未入库的 symbol 请求腾讯主数据。
- 公告：巨潮按市场分页增量同步，6 小时 overlap；每市场每上海自然日额外复核最多一个历史日期桶，首次严格 warm-up 约 30 个成功维护日，不逐股拉取。
- 重大公告正文：仅在部署机存在 `pdftotext` 且资源门禁为 normal 时运行；每 5 分钟最多 4 份、每日最多 20 次请求 / 64 MiB、单 PDF 最大 8 MiB。解析器缺失时零下载，严格 analysis readiness 保持 blocked。
- 进度 flush：每 50 个 symbol 批量写入一次（减少 DuckDB 写入次数）。
- DuckDB 写入：单 writer（`SetMaxOpenConns(1)`），批量 upsert。
- 全市场 Universe 维护与全市场日 K 任务共享进程内 single-flight，check/create 原子化；同一服务进程不会同时发起两套重复全市场请求。
- 历史资金流修补：每 5 分钟最多 10 个候选、每天最多 300 次真实联网请求；预算在联网前持久化预占，重启不会突破上限，持仓/策略保留优先份额但不能饿死游标队列。
- AI 总结并发：当前 2 vCPU / 3.5 GiB 主机使用 1 个持久队列 worker，单 symbol 在途去重，lease 超时自动恢复；资源门禁为 `normal` 才启动新任务。
- DuckDB 资源：`memory_limit = '768MB'`，`threads = 1`，单 writer；SQLite 连接上限为 4、空闲连接为 2。
- 资源门禁：内存、load1 或磁盘进入受限状态时降低或暂停后台重任务；门禁状态通过 readiness overview 暴露，不把资源暂停误报为维护成功。

任务进度明确拆为 `maintenanceProgress.coverage`、`maintenanceProgress.assets` 和 `maintenanceProgress.aiProfile`。覆盖检查完成后，基础资产仍可显示 stale/retrying，AI 也可继续显示 queued/running/retry；前端复用任务快照，不增加逐标的请求。

### 11.3 DuckDB 写入策略

DuckDB 写入应：

- 批量 upsert。
- 按 symbol 或固定条数 flush，例如 50 到 100 个 symbol 一批。
- 避免每条 bar 单独事务。
- 大批量写入后周期性 checkpoint。
- 不在同一时间启动多个大批量写任务。

### 11.4 失败退避

每个 symbol 和每类数据源都需要失败退避：

- 记录最近失败原因。
- 记录连续失败次数。
- 计算 `next_retry_at`。
- 未到 retry 时间跳过该 source，但不影响其他 source。

避免单个坏 symbol 或坏 source 拖垮整轮维护。

## 12. 前端一致性

后端重构必须同步前端，不允许只改后端。

### 12.1 导航收敛

StockV2 的“数据资产”二级页建议收敛为：

- `资产总览`
- `维护任务`
- `维护配置`
- `公告 / 重大事项`
- `画像记录`

移除独立“画像配置”页。旧配置不再读写；历史数据库中残留的旧列不再构成产品行为。

### 12.2 维护任务视图

维护任务页应展示分项进度：

```text
标的扫描     320 / 7158
日 K补缺     48 成功 / 2 失败
基础画像     31 变化 / 289 无变化
公告检查     12 新增 / 3 重大
AI总结       5 已发起 / 1 失败 / 26 跳过
```

任务明细表按 symbol 展示：

- 标的。
- 优先级原因。
- 日 K 状态。
- 基础画像状态。
- 公告新增数。
- 重大事项数。
- AI 决策。
- 错误摘要。
- 耗时。

### 12.3 标的列表

标的列表应展示：

- 日 K：完整 / 缺口 / 陈旧 / 失败。
- 基础画像：ready / changed / missing。
- 公告：最近公告时间、新增数量、重大事项数量。
- AI：missing / ready / running / failed / skipped。

### 12.4 单标的详情

单标的详情中的“更新画像”应改成“维护该标的数据资产”或相近表达。

触发后应显示：

- 日 K 补缺结果。
- 基础画像是否变化。
- 新公告 / 重大事项。
- AI 是否发起。
- 关联 AgentRun / DecisionLedger。

### 12.5 文案修正

需要移除或改写以下旧心智文案：

- “基础输入变化时才启动画像 AI。”
- “画像配置。”
- “每轮标的数 / 每标的 AI 轮数。”
- “深度画像队列。”

替换为：

- “基础画像变化、新公告 / 重大事项或 AI 缺失时触发总结。”
- “统一维护预算。”
- “AI 总结预算。”
- “资产维护明细。”

## 13. 与现有模块的关系

### 13.1 与新闻链路

新闻链路继续负责高召回消息事件和候选关联。

公告链路负责强来源、可归档、可追溯的信息面资产。

两者都可以进入：

- 组合哨兵。
- 策略生成。
- 机会发现。
- 单标的画像总结。

### 13.2 与 embedding

基础画像、AI 画像或公告摘要变化后，应标记相关股票画像 embedding stale。

embedding 生成仍由独立向量资产维护执行，不应阻塞数据资产维护主任务。

#### 13.2.1 向量检索优化（DuckDB 原生计算）

`stockv2_embedding_vectors_v2` 表新增 `vector_values DOUBLE[]` 列，存储原生向量数组，与 `vector_blob`（旧格式 BLOB）共存。

**写入**：`UpsertEmbeddingVector` 时同时写入 `vector_blob`（兼容旧数据）和 `vector_values`（新格式）。

**检索**：`SearchEmbeddingVectors` 优先使用 `vector_values` 列，通过 SQL 层 `list_transform + list_sum + sqrt` 手动计算 cosine similarity + ORDER BY + LIMIT，在 DuckDB 内完成 TopK 计算，避免全量加载到 Go 层。

```sql
WITH query_vec AS (SELECT ?::DOUBLE[] AS qv)
SELECT e.vector_ref, e.object_type, e.object_id,
    list_sum(list_transform(e.vector_values, (x, i) -> x * query_vec.qv[i]))
    / (sqrt(list_sum(list_transform(e.vector_values, x -> x*x)))
       * sqrt(list_sum(list_transform(query_vec.qv, x -> x*x))))
    AS score
FROM stockv2_embedding_vectors_v2 e, query_vec
WHERE e.model_id = ? AND e.vector_values IS NOT NULL
ORDER BY score DESC
LIMIT ?
```

> 注：DuckDB 的 `array_cosine_distance` 要求 `DOUBLE[ANY]` 类型参数，与列存储的 `DOUBLE[]` 类型不匹配，因此使用手动展开计算。

**回退**：旧数据（只有 `vector_blob`，`vector_values IS NULL`）通过 `searchEmbeddingVectorsBlob` 在 Go 层解码计算。两条路径结果通过 `mergeEmbeddingHits` 合并去重。

**迁移**：`ALTER TABLE stockv2_embedding_vectors_v2 ADD COLUMN IF NOT EXISTS vector_values DOUBLE[]`，旧数据保持 NULL，下次 embedding 重建时自动填充。

### 13.3 与 Agent

Agent 只负责画像总结，不负责采集数据。

主程序负责：

- 数据采集。
- 去重。
- diff。
- 触发判断。
- 预算控制。
- 输出校验。

Agent 负责：

- 基于已提供上下文更新长期画像总结。
- 输出关键词、业务线、风险标签和 source notes。
- 明确不确定性。

### 13.4 下游可用性门禁

下游不能再以“记录存在”代替“资产新鲜”。统一 readiness 分为：

- `market_ready`：权威交易日历新鲜，最近已收盘交易日及日 K 核心 / 股票资金流字段完整。
- `message_ready`：基础画像复核未过期，公告增量 cursor 新鲜且迟到公告复核完成 warm-up。
- `analysis_ready`：前两项满足，AI 已应用最新目标版本，重大公告正文不存在未完成状态。

策略生成默认使用 strict analysis 门禁；显式 `allow_degraded` 才可继续，并把失败标的、原因和限制写入返回值与持久化运行记录。组合哨兵和操作复盘同样持久化其 degraded 决策，不能静默消费陈旧资产。第三方失败、资源门禁暂停或本机缺少正文解析器时保持 blocked / stale / retrying，而不是标记维护成功。

## 14. 迁移计划

### 阶段一：统一维护明细和画像触发

- 新增 `stockv2_asset_maintenance_items`。
- 将基础画像刷新并入统一维护 per-symbol pipeline。
- AI 触发条件加入 `ai missing`。
- 前端移除独立画像配置展示，改为统一维护预算。
- 保留旧画像记录读取，但标记为历史。

### 阶段二：公告 / 重大事项资产

- 新增 `stockv2_announcements`。
- 接入公告增量源。
- 实现重大事项标题 / 分类规则。
- 统一维护中写入公告并触发 AI。
- 前端新增公告 / 重大事项页和单标的公告摘要。

### 阶段三：日 K 缺口和数据面字段

- 已增强日 K 质量评估，识别中间缺口和字段不完整行。
- 已写入换手率、成交额、主力 / 分档净流入及 presence；无可靠来源的买入 / 卖出额不伪造。
- 已在前端展示数据面覆盖状态。
- 已将日 K 数据面摘要加入 Agent 画像上下文。

### 阶段四：资源治理和回收旧逻辑

- 引入 per-source 失败退避。
- 引入统一维护预算和并发限制。
- 移除旧深度画像队列调度代码。
- 旧设置字段停止展示并停止读写。
- 补齐测试覆盖。

## 15. 验收标准

功能验收：

- 持仓标的优先维护，不再长期排在全市场队列后面。
- 无 AI 画像总结的标的会在维护时触发总结。
- 基础画像变化会触发 AI。
- 新公告 / 重大事项会触发 AI。
- 日 K 只补缺口，不无意义全量覆盖。
- 公告资产可在前端看到，且可追溯来源。

稳定性验收：

- 单个 symbol 失败不影响整轮任务。
- 单个 source 失败有退避，不会无限重试。
- DuckDB 批量写入不会长时间阻塞前端读取。

前端验收：

- 数据资产页能解释当前维护状态。
- 任务历史能看到每个 symbol 的分项结果。
- 不再出现独立“深度画像队列”产品心智。
- 单标的详情能看到日 K、画像、公告和 AI 总结之间的关系。

## 16. 测试要求

后端至少覆盖：

- 日 K 完全缺失、尾部缺失、中间缺口、无需维护。
- 基础画像 hash 变化 / 不变化。
- AI missing 触发。
- 基础画像变化触发。
- 新公告触发。
- 公告去重。
- AI 预算耗尽跳过。
- source 失败退避。
- DuckDB 批量 upsert 幂等。

前端至少覆盖：

- 维护任务分项状态渲染。
- 旧配置隐藏，新统一配置展示。
- 单标的维护结果展示。
- 公告 / 重大事项空状态、加载态、错误态。

## 17. 实现注意事项

- 不要把公告和普通新闻混在同一张表。
- 不要继续依赖 `status=active` 表达可维护范围。
- 不要让 AI 自己决定是否采集公告或补日 K。
- 不要在维护任务里并发启动大量 AgentRun。
- 不要把全量市场重爬作为默认路径。
- 不要只改后端。前端导航、配置、任务历史和单标的详情必须同步。
- 废弃旧逻辑时不要继续通过兼容层驱动新行为；历史数据库可保留旧列，但代码不再依赖。

# 相关股票信息获取接口

下面是带接口地址和测试格式的版本, 由于本服务部署在海外, 所以部分接口无法使用. 以下为经过测试的接口:

  1. 腾讯日 K fqkline

  用途：日 K 缺口补齐。

  测试 URL：

  https://web.ifzq.gtimg.cn/appstock/app/fqkline/get?param=sz002457,day,2026-07-01,2026-07-08,20,

  格式：

  param={marketPrefix}{code},day,{startDate},{endDate},{count},{fq}

  示例：

  param=sz002457,day,2026-07-01,2026-07-08,20,

  返回结果：成功，HTTP 200，返回 6 根日 K。

  字段形态：

  ["2026-07-08", "14.930", "15.480", "16.300", "14.140", "870732.000"]

  含义：

  date, open, close, high, low, volume

  限制：不提供成交额、换手率、资金流。

  2. 腾讯实时行情

  用途：实时价、涨跌幅、换手率等轻量字段。

  测试 URL：

  https://qt.gtimg.cn/q=sz002457

  格式：

  q={marketPrefix}{code}

  结果：成功，HTTP 200，返回 88 个 ~ 分隔字段。

  本次样例字段：

  字段 3  = 15.48  最新价
  字段 32 = 3.27   涨跌幅
  字段 38 = 26.13  换手率

  这个适合高频/批量，优先级高于东财实时类接口。

  3. 东财 F10 公司概况

  用途：公司全称、简称、行业、公司简介、经营范围。

  测试 URL：

  https://emweb.securities.eastmoney.com/PC_HSF10/CompanySurvey/CompanySurveyAjax?code=SZ002457

  格式：

  code={MARKET}{code}

  示例：

  code=SZ002457

  结果：成功。

  关键字段：

  jbzl.gsmc      公司全称
  jbzl.agjc      A股简称
  jbzl.sshy      所属行业
  jbzl.sszjhhy   证监会行业
  jbzl.gsjj      公司简介
  jbzl.jyfw      经营范围

  4. 东财 F10 主营构成

  用途：主营业务构成、收入占比、毛利率等。

  测试 URL：

  https://emweb.securities.eastmoney.com/PC_HSF10/BusinessAnalysis/PageAjax?code=SZ002457

  格式：

  code={MARKET}{code}

  结果：成功，返回 200 条。

  关键字段：

  zygcfx[].REPORT_DATE
  zygcfx[].MAINOP_TYPE
  zygcfx[].ITEM_NAME
  zygcfx[].MAIN_BUSINESS_INCOME
  zygcfx[].MBI_RATIO
  zygcfx[].GROSS_RPOFIT_RATIO

  样例业务：

  非金属矿物制品业
  水利技术专业服务业

  5. 东财 F10 核心题材 / 板块

  用途：重点板块、概念、主营业务、行业背景。

  测试 URL：

  https://emweb.securities.eastmoney.com/PC_HSF10/CoreConception/PageAjax?code=SZ002457

  格式同上：

  code=SZ002457

  结果：成功，返回 20 个板块、12 个核心题材。

  关键字段：

  ssbk[].BOARD_NAME      板块名
  hxtc[].KEYWORD         关键词
  hxtc[].MAINPOINT_CONTENT 说明正文
  hxtc[].KEY_CLASSIF     分类

  样例：

  板块：建筑材料、装修建材、管材、宁夏板块
  主营：高品质输水管道及相关产品的研发、生产、销售

  6. 巨潮 orgId 映射

  用途：公告查询前拿真实 orgId。

  测试 URL：

  http://www.cninfo.com.cn/new/data/szse_stock.json

  结果：成功，约 6209 条。

  002457 命中：

  {
    "code": "002457",
    "orgId": "9900013690",
    "zwjc": "青龙管业"
  }

  7. 巨潮公告查询

  用途：公告、重大事项披露。

  测试 URL：

  https://www.cninfo.com.cn/new/hisAnnouncement/query

  测试 POST body：

  stock=002457,9900013690
  &tabName=fulltext
  &pageSize=10
  &pageNum=1
  &column=szse
  &category=
  &plate=sz
  &seDate=
  &searchkey=
  &secid=
  &sortName=
  &sortType=
  &isHLtitle=true

  关键点：stock 必须是：

  {code},{orgId}

  结果：成功，totalAnnouncement=2095，最近 10 条正常返回。

  样例：

  2026-07-08 关于股票交易异常波动公告
  2026-07-07 关于签订买卖合同的公告
  2026-07-02 关于收到拟中标通知书的公告

  PDF 地址格式：

  https://static.cninfo.com.cn/{adjunctUrl}

  测试样例：

  https://static.cninfo.com.cn/finalpage/2026-07-08/1225414335.PDF

  结果：HTTP 200，Content-Type: application/pdf。

  8. 东财日级资金流

  用途：日级主力、大单、中单、小单、超大单净流入。

  测试 URL：

  https://push2his.eastmoney.com/api/qt/stock/fflow/daykline/get?lmt=20&klt=101&secid=0.002457&fields1=f1,f2,f3,f7&fields2=f51,f52,f53,f54,f55,f56,f57,f58,f59,f60,f61,f62,f63

  格式：

  secid={marketCode}.{code}

  其中：

  沪市 1.xxxxxx
  深市 0.xxxxxx

  结果：成功，返回 20 条。

  样例行：

  2026-07-08,-7063286.0,-8017200.0,15080480.0,6673264.0,-13736550.0,-0.54,-0.61,1.15,0.51,-1.05,15.48,3.27

  前几个字段可按：

  date, main_net, small_net, mid_net, large_net, super_net, ...

  注意：东财 push2his 可用，但要限速和失败退避。

  9. 不建议第一版依赖的接口

  东财 slist 板块接口：

  https://push2.eastmoney.com/api/qt/slist/get?fltt=2&invt=2&secid=0.002457&spt=3&pi=0&pz=200&po=1&fields=f12,f14,f3,f128

  测试结果：当前网络返回 302 且 body 为空。

  东财 stock/get：

  https://push2.eastmoney.com/api/qt/stock/get?secid=0.002457&fields=f43,f44,f45,f46,f47,f48,f49,f50,f57,f58,f84,f85,f116,f117,f168,f169,f170

  测试结果：当前网络出现 502 / 空响应。

  结论：这两个不放进核心路径。板块优先用东财 F10 CoreConception，实时字段优先用腾讯。
