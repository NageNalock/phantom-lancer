# 股票 V2 机会发现技术设计

> 文档日期：2026-06-26
>
> 状态：V2 开发前技术设计
>
> REPLACES：无。本文补充 `stock-agent-workbench-v2-key-points-2026-06-18.md` 中 `Opportunity`、`Theme`、`GeneratedStrategy`、`AgentRun` 和 `DecisionLedger` 的机会发现落地细节。
>
> 相关文档：`docs/stock-v2-strategy-generation-design-2026-06-26.md` 定义候选进入策略生成后的 `strategy_generation` 流程；`docs/stock-v2-opportunity-market-scan-design-2026-08-10.md` 定义服务端先做确定性主板预筛、再复用本任务做有界候选复核的自动扫描模式。

## 1. 目标

机会发现解决的是“用户知道一个主题、事件或判断，但不知道应该关注哪只股票，也不知道策略是什么”的场景。

典型输入：

```text
字节跳动的新 AI 模型很好，帮我找 A 股 / ETF 里的相关机会，并给出后续策略方向。
```

目标链路：

```text
用户输入主题
-> Opportunity
-> OpportunityDiscoveryRun
-> Codex CLI 研究执行
-> MCP 查询项目内资料
-> Codex CLI 自行搜索外部公开资料
-> 结构化步骤 / 证据 / 候选留痕
-> OpportunityCandidate 候选池
-> 用户选择候选
-> strategy_generation
-> 策略草案
```

本设计面向完整可用闭环：主题到候选池、项目内资料召回、外部公开资料研究、向量召回、候选进入策略生成都应落地。不要把外部搜索实现进主程序；外部公开资料搜索仍由 Codex CLI 自身能力完成。

## 2. 核心原则

手工主题研究由 Codex CLI 作为研究执行者闭环完成，主程序不替 Agent 做完整候选计算。`mode=market_scan` 是明确例外：主程序先按固定预算做确定性全市场预筛，Agent 只在服务端提供的候选子集中完成公开资料验证、风险检查和最终排序。

主程序只负责：

- 创建机会、运行记录和任务上下文。
- 提供受控的项目内数据 MCP 查询工具。
- 提供 MCP 过程记录与最终结果回填工具。
- watch Codex CLI 的可见执行过程，并写入 Agent 运行留痕。
- 校验 Agent 回填结果并落库。
- 提供清晰的前端可观测视图。

外部公开资料搜索由 Codex CLI 自己完成。主程序不提供 `web_search` / `web_fetch` MCP 工具，避免做出比 CLI 自带能力更窄的搜索实现。

向量召回依赖已绑定的嵌入模型。DuckDB 只负责存储和相似度搜索向量，不负责把文本生成 embedding。股票画像、相关新闻、主题文本等 embedding 必须通过 StockV2 Agent 模型配置里的 `modelType=embedding` 模型生成。

如果未绑定嵌入模型，或绑定模型被禁用 / 不可用，所有需要 embedding 的能力必须在上层入口直接拦截并返回明确错误，不进入后台任务、MCP 工具或降级为假向量结果。关键词搜索可以继续作为独立能力存在，但不能伪装成语义向量召回。

## 3. 能力边界

允许：

- Agent 查询项目内股票、ETF、画像、行情、日 K、新闻、已有策略和组合上下文。
- Agent 使用 Codex CLI 自身搜索 / 浏览能力查外部公开资料。
- Agent 通过 MCP 记录研究步骤、外部来源、证据、候选和最终结论。
- 主程序把通过校验的候选落库。
- 用户从候选进入策略生成。

禁止：

- Agent 直接写数据库。
- Agent 直接改持仓。
- Agent 直接创建操作单。
- Agent 直接激活策略。
- Agent 读取 token、cookie、私有配置或本地敏感文件。
- Agent 把外部网页搜索结果当成已核验事实；必须保留来源和不确定性。
- 未绑定可用嵌入模型时启动 embedding 生成、向量索引重建或语义向量召回。

## 4. 数据模型

### 4.1 stockv2_opportunities

表示一个长期存在的主题机会。

```text
id
title
user_thesis
market_scope              // a_share | hk | us | all，当前实现范围至少覆盖 a_share
instrument_scope          // stock | exchange_fund | both
status                    // draft | researching | completed | closed
created_by
created_at
updated_at
```

说明：

- `title` 是短标题，例如“字节跳动 AI 模型主题”。
- `user_thesis` 保留用户原始判断。
- `status=completed` 只表示本轮机会发现完成，不代表机会一定成立。

### 4.2 stockv2_opportunity_discovery_runs

表示一次机会发现运行。

```text
id
opportunity_id
agent_run_id
status                    // pending | running | completed | failed | cancelled
current_step_id
step_total
step_completed
candidate_count
evidence_count
external_source_count
started_at
finished_at
error_message
created_at
updated_at
```

说明：

- 一个 Opportunity 可以有多次 discovery run。
- `agent_run_id` 关联现有 AgentRun / DecisionLedger。
- `current_step_id` 用于前端展示当前进度。

### 4.3 stockv2_opportunity_discovery_steps

表示 Agent 研究过程中的结构化步骤。

```text
id
run_id
step_key                  // understand_theme | internal_recall | external_research | ...
step_title
status                    // pending | running | completed | failed
order_index
input_summary
output_summary
metadata_json
started_at
finished_at
created_at
updated_at
```

机会发现固定 8 个步骤：

```text
1. understand_theme        主题理解
2. internal_recall         项目内资料召回
3. external_research       外部公开资料搜索
4. theme_chain             产业链 / 主题链条拆解
5. candidate_merge         候选合并与去噪
6. market_risk_check       行情与风险检查
7. candidate_ranking       候选排序
8. final_report            最终报告
```

### 4.4 stockv2_opportunity_evidence

表示候选和结论的证据来源。

```text
id
run_id
candidate_id              // nullable
source_type               // internal_profile | internal_news | quote | daily_bar | external_source | agent_note
source_ref                // symbol / news_event_id / URL hash / agent note id
title
summary
url                       // nullable
publisher                 // nullable
published_at              // nullable
confidence
metadata_json
created_at
```

说明：

- 外部来源 URL 写入前必须去掉敏感 query。
- 外部来源只代表 Agent 看到过该来源，不代表来源内容一定正确。

### 4.5 stockv2_opportunity_candidates

表示一次机会发现产出的股票 / ETF 候选。

```text
id
opportunity_id
run_id
symbol
market
instrument_type           // stock | exchange_fund
name
relation_type             // direct | supply_chain | theme_etf | competitor | weak
relevance_score
evidence_score
market_risk_score
confidence
rank
status                    // candidate | shortlisted | rejected | strategy_requested | strategy_generated
reason
risk_summary
metadata_json
created_at
updated_at
```

说明：

- `relevance_score` 表示与主题的相关性。
- `evidence_score` 表示证据强度。
- `market_risk_score` 表示行情过热、数据缺失、流动性等风险。
- 主程序只接受主数据中已存在的股票 / ETF。无法解析的 symbol 不落为有效候选。

### 4.6 stockv2_opportunity_results

表示一次运行的最终报告。

```text
id
run_id
summary
conclusion
recommended_next_action
raw_result_json
created_at
```

### 4.7 stockv2_embedding_assets

表示 StockV2 本地可检索的向量资产。向量可以存储在 DuckDB，操作状态仍可由 SQLite 侧记录；实现时保持 repo / service 边界清晰。

```text
id
object_type               // stock_profile | news_event | raw_news | opportunity | external_source
object_id
text_hash
text_summary
model_id
provider_id
embedding_protocol        // openai_embeddings | volcengine_multimodal_embeddings
embedding_dimensions
vector_ref                // DuckDB row key 或内部引用
status                    // ready | stale | failed
error_message
created_at
updated_at
```

说明：

- 同一批可比较向量必须来自同一个 embedding model 和相同 dimensions。
- 嵌入模型切换后，旧向量不得继续混用；应标记为 `stale` 并提示重建。
- `stock_profile` 和 `news_event` 是机会发现的第一优先级向量对象。

### 4.8 stockv2_embedding_work_items

表示源对象变更后等待向量同步的持久队列。股票画像、新闻事件、主题、主题版本和机会对象在语义内容发生变化时写入一条工作项；自动维护只点查这些对象，不得为了确认“没有任务”而周期性遍历全部历史源表。

```text
object_type
object_id
revision                    // 同一对象再次变化时递增
enqueued_at
```

说明：

- `(object_type, object_id)` 唯一；维护完成时必须按读取到的 `revision` 条件删除，避免处理期间发生的新变化被旧任务覆盖。
- 主题和主题版本的 `index_status` 同时作为崩溃恢复来源；启动或定时维护可以把非 `ready` ID 补入队列，但只能读取有界 ID 列表。
- 只有 owner 显式发起的强制重建可以遍历历史源表，并且取满本轮数量上限后必须立即停止扫描。

### 4.9 stockv2_embedding_config

表示 StockV2 向量化能力绑定的嵌入模型。

```text
id
embedding_model_id
enabled
last_probe_at
last_probe_status
last_error
updated_at
```

说明：

- `embedding_model_id` 必须指向 `AgentModelProfile.modelType=embedding`。
- 模型必须 `enabled=true` 且 `status=available` 才能用于 embedding 生成和向量召回。
- 该配置不同于 AgentTaskProfile。嵌入模型不能绑定到 `operation_review`、`strategy_generation` 等 chat Agent task。

## 5. MCP 工具

机会发现复用现有 `stock_agent` MCP server。扩展现有 MCP 工具即可，不引入新的 MCP server。

### 5.1 项目内资料查询

```text
stock_agent.search_instruments
stock_agent.search_stock_profiles
stock_agent.semantic_search_stock_profiles
stock_agent.get_stock_profile
stock_agent.get_latest_quotes
stock_agent.get_daily_bars_summary
stock_agent.search_news_events
stock_agent.semantic_search_news_events
stock_agent.search_news_link_candidates
stock_agent.list_existing_strategies
stock_agent.get_portfolio_context
stock_agent.get_embedding_status
```

查询工具必须只返回业务所需字段，不返回 token、cookie、私有配置和数据库路径。

`semantic_search_stock_profiles` 和 `semantic_search_news_events` 是向量检索工具，调用前必须检查嵌入模型绑定和向量索引状态：

- 未绑定嵌入模型：返回 `embedding_model_not_configured`。
- 嵌入模型不可用：返回 `embedding_model_unavailable`。
- 向量资产为空或 stale：返回 `embedding_asset_not_ready`，并提示先重建。
- 不允许静默回退到关键词搜索；关键词搜索应由 `search_stock_profiles` / `search_news_events` 明确完成。

### 5.2 过程记录

```text
stock_agent.start_discovery_step
stock_agent.finish_discovery_step
stock_agent.fail_discovery_step
stock_agent.record_external_source
stock_agent.record_evidence
stock_agent.record_candidate
stock_agent.update_candidate
stock_agent.submit_result
```

`record_external_source` 示例：

```json
{
  "runId": "run_xxx",
  "stepId": "step_xxx",
  "title": "source title",
  "url": "https://example.com/article",
  "publisher": "example",
  "publishedAt": "2026-06-26T10:00:00Z",
  "summary": "source summary",
  "relatedSymbols": ["300000"],
  "confidence": 0.7
}
```

`submit_result` 仍采用 taskID 校验。`taskType` 必须是 `opportunity_discovery`。

## 6. Codex CLI Executor

新增 `opportunity_discovery` 执行分支。

执行输入：

- `taskID`
- `opportunityID`
- 用户主题和范围设置
- MCP server 信息
- 输出 schema
- 安全边界

Prompt 必须明确：

```text
你是股票机会发现 Agent。
你必须主动使用 Codex CLI 自身搜索 / 浏览能力查公开资料。
不要只依赖项目内 MCP 数据。
项目内数据只能通过 stock_agent MCP 查询。
每个阶段必须通过 MCP 记录 start / finish。
每个外部来源必须通过 MCP 记录 record_external_source。
每个候选必须有内部证据或外部证据。
当需要语义向量召回时，必须先调用 get_embedding_status；如果未绑定可用嵌入模型，应记录步骤失败并解释无法进行向量召回。
最终只能生成候选和策略草案建议，不能生成操作单，不能改持仓，不能激活策略。
```

当前 executor 如果使用隔离配置，需要验证 Codex CLI 自带搜索 / 浏览能力是否仍可用。若隔离配置屏蔽了搜索能力，应为 `opportunity_discovery` 增加单独的 executor 配置：

- 只注入 `stock_agent` MCP。
- 不加载无关用户 MCP。
- 保留 CLI 内建搜索 / 浏览能力。
- 继续捕获 stdout、stderr 和 JSON event transcript。

## 7. Agent 输出 Schema

`submit_result.result.outputType`：

```text
opportunity_discovery
```

`result.result`：

```json
{
  "schema_version": "opportunity-discovery-report/v1",
  "opportunity_id": "opp_xxx",
  "summary": "本次机会发现摘要",
  "theme_chain": [
    {
      "layer": "上游稀缺供给",
      "rank": 1,
      "representatives": ["600000"],
      "scarcity": "产能扩张周期长，供给弹性有限"
    }
  ],
  "candidates": [
    {
      "symbol": "300000",
      "market": "SZ",
      "name": "示例股票",
      "instrument_type": "stock",
      "relation_type": "supply_chain",
      "rank": 1,
      "relevance_score": 82,
      "evidence_score": 70,
      "market_risk_score": 45,
      "confidence": 0.72,
      "reason": "相关原因",
      "risk_summary": "主要风险",
      "suggested_strategy_intent": "建议后续策略生成关注点"
    }
  ],
  "excluded": [
    {
      "symbol": "300001",
      "reason": "证据不足或相关性弱"
    }
  ],
  "data_quality_notes": [],
  "external_sources": []
}
```

主程序校验规则：

- `schema_version` 必须为 `opportunity-discovery-report/v1`。
- `opportunity_id` 必须匹配当前 run。
- 候选 `symbol` 必须存在于 StockV2 主数据。
- 分数必须在 0 到 100 之间。
- `confidence` 必须在 0 到 1 之间。
- 不允许包含操作单、持仓修改或策略激活指令。
- 如果候选声称来自语义向量召回，必须能追溯到对应 embedding model、向量资产和 evidence 记录。

## 8. 后端 API

API：

```text
GET    /api/stockv2/opportunities
POST   /api/stockv2/opportunities
GET    /api/stockv2/opportunities/{id}
PATCH  /api/stockv2/opportunities/{id}
POST   /api/stockv2/opportunities/{id}/discovery-runs
GET    /api/stockv2/opportunities/{id}/discovery-runs
GET    /api/stockv2/opportunity-discovery-runs/{id}
GET    /api/stockv2/opportunity-discovery-runs/{id}/steps
GET    /api/stockv2/opportunity-discovery-runs/{id}/evidence
GET    /api/stockv2/opportunity-discovery-runs/{id}/candidates
POST   /api/stockv2/opportunity-candidates/{id}/generate-strategy
PATCH  /api/stockv2/opportunity-candidates/{id}
GET    /api/stockv2/embeddings/status
PATCH  /api/stockv2/embeddings/config
POST   /api/stockv2/embeddings/rebuild
GET    /api/stockv2/embeddings/assets
```

列表接口统一支持 `limit` / `offset`，前端做分页。

`generate-strategy` 调用已有 `strategy_generation`，并把以下上下文传入：

```text
opportunityId
candidateId
candidate reason
candidate scores
evidence refs
external source summaries
```

`StrategyGenerationContext` 需要补充：

```text
Opportunity
OpportunityCandidate
OpportunityEvidence[]
```

## 9. 运行流程

```text
1. 用户创建 Opportunity。
2. 用户点击“开始发现”。
3. 主程序创建 OpportunityDiscoveryRun。
4. 主程序创建 AgentRun(task_type=opportunity_discovery)。
5. 主程序启动 Codex CLI。
6. Codex CLI 调 MCP start_discovery_step。
7. Codex CLI 调项目内资料 MCP 查询。
8. Codex CLI 使用自身搜索 / 浏览能力查外部资料。
9. Codex CLI 如需语义召回，先调 get_embedding_status；状态可用后再调 semantic_search_*。
10. Codex CLI 调 record_external_source / record_evidence / record_candidate。
11. Codex CLI 调 submit_result。
12. 主程序校验结果。
13. 主程序落库结果，更新 run 状态。
14. 用户查看候选和证据。
15. 用户选择候选进入 strategy_generation。
```

失败处理：

- Codex CLI 失败：run 标记 `failed`，保留 transcript 和已完成步骤。
- MCP 回填缺失：run 超时后标记 `failed`，保留 transcript。
- 候选校验失败：该候选标记 rejected，run 可继续完成。
- 外部来源记录失败：不阻断任务，但 step 输出必须写明证据缺口。
- 未绑定可用嵌入模型：embedding 生成、向量重建和 semantic_search MCP 在入口直接失败；机会发现可以继续使用关键词、项目内资料和外部搜索，但必须在步骤输出中标记“未执行向量召回”。

## 10. 前端可观测性

新增“机会发现”二级页面或 StockV2 下的独立视图。

主界面：

```text
左侧：Opportunity 列表
中间：机会详情、候选池
右侧：最近运行状态 inspector
```

运行详情 Drawer：

```text
顶部状态条：
状态、当前步骤、进度、候选数、证据数、外部来源数、耗时。

中部 Step Timeline：
✓ 主题理解
✓ 项目内资料召回
● 外部公开资料搜索
○ 产业链拆解
○ 候选合并与去噪
○ 行情与风险检查
○ 候选排序
○ 最终报告

右侧 Step Detail：
输入摘要、输出摘要、外部来源、内部 MCP 查询、候选变化、原始 CLI 片段。
```

候选池：

```text
Rank
股票 / ETF
关系类型
相关性
证据强度
市场风险
置信度
证据数
操作：生成策略 / 标记排除 / 查看证据
```

候选详情 Drawer：

```text
为什么相关
支持证据
反对与风险
行情摘要
日 K 摘要
相关新闻
外部来源
是否已有策略
语义召回来源、embedding model、向量资产状态
```

UI 验收标准：

- 能一眼看出当前运行做到哪一步。
- 能看出每一步的输入和输出。
- 能看出候选为什么被选中或排除。
- 能从候选追溯到内部证据和外部来源。
- 能打开原始 Agent run / DecisionLedger 查看 transcript。
- 当嵌入模型未绑定时，向量召回和重建入口应显示明确不可用原因，而不是出现空结果。

## 11. 安全与留痕

必须保留：

- Codex CLI transcript。
- MCP tool call。
- step input / output。
- external source URL / summary。
- candidate score 变化。
- final structured result。

脱敏要求：

- 不保存 cookie、token、authorization、API key。
- 外部 URL 保存前去掉敏感 query。
- stdout / stderr 继续走现有 redaction 和长度裁剪。
- 外部网页内容只保存摘要，不保存全文。
- embedding 输入文本和模型返回内容写入前要裁剪；不保存 provider credential、请求 header 或完整敏感响应。

权限边界：

- MCP 查询工具只暴露 StockV2 业务数据。
- MCP 写入工具只写入当前 run 的 step / evidence / candidate / result。
- `taskID` / `runID` 必须匹配当前运行。
- 主程序最终校验通过后才落库为正式候选。
- embedding 相关 API、后台任务和 MCP 工具必须先解析 `stockv2_embedding_config.embedding_model_id`，并校验模型类型、启用状态、可用状态和 dimensions。

## 12. 完整交付范围

本功能开发不再分批交付。以下内容都属于本轮完整范围。

- Opportunity / Run / Step / Evidence / Candidate / Result 表。
- Opportunity CRUD。
- Opportunity discovery run 启动。
- Codex CLI `opportunity_discovery` executor。
- MCP step / evidence / candidate / submit_result。
- CLI transcript 保存。
- 前端机会发现页面、Step Timeline、候选池。
- 项目内资料 MCP 查询工具补齐。
- 候选详情 Drawer。
- 从候选调用 `strategy_generation`。
- `StrategyGenerationContext` 带入 opportunity / candidate / evidence。
- 策略草案显示其来源机会。
- 嵌入模型绑定配置、状态检查和测试结果展示。
- 股票画像、相关新闻、机会主题的 embedding 生成任务。
- 股票画像向量资产。
- 新闻事件向量资产。
- 主题与股票画像相似度召回。
- 主题与新闻事件相似度召回。
- 候选排序增强。
- 用户反馈回流到候选排序。
- 记忆参与机会发现。
- 未绑定嵌入模型时，上层 UI / API / MCP 对 embedding 能力直接拦截并展示明确原因。

关键词搜索、股票画像搜索、消息候选和外部搜索仍是独立能力；它们可以在没有嵌入模型时继续工作，但不得标记为向量召回。
