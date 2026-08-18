# 股票 V2 策略生成能力设计

> 文档日期：2026-06-26
>
> 状态：V2 开发前设计
>
> REPLACES：无。本文补充 `stock-agent-workbench-v2-key-points-2026-06-18.md` 中 `GeneratedStrategy`、`StrategyObj`、`StrategyPlaybook`、`StrategyPatch` 的策略生成细节。
>
> 参考来源：当前 StockV2 对象网络；`docs/stock-v2-model-horizon-outlook-design-2026-08-18.md` 的模型多周期预期；StockPulse 的持仓操作策略 workflow 思路。本文只吸收流程思想，不沿用 StockPulse 旧表、旧 API 或旧页面结构。

## 1. 目标

`strategy_generation` 是股票 V2 的统一策略生成能力。

它负责把人工目标、机会、单只股票或当前组合持仓，转成可确认、可监控、可 Review 的策略草案。

推荐链路：

```text
输入目标 / 机会 / 当前持仓
-> AgentRun / DecisionLedger
-> StrategyGenerationContext
-> Agent 生成策略草案
-> stockv2_strategies(status=draft, source=agent)
-> 用户确认激活
-> StrategyVersion / generationMeta.playbook.rules
-> 后台监控
-> MonitorHit
-> OperationReview
```

策略生成的核心产物不是自然语言报告，而是当前代码已支持的 `generationMeta.playbook.rules`：一组可被后台监控消费的动作规则。

## 2. 能力边界

策略生成只生成策略草案，不自动下单，不直接改持仓，不直接创建人工盯盘任务。

正式策略必须经过用户确认：

```text
stockv2_strategies(status=draft, source=agent)
-> 用户确认 / 归档
-> stockv2_strategies(status=active / archived)
-> StrategyVersion
```

账户绑定策略可以产生账户相关建议，但只有进入 `OperationReview` 并通过 `Guardrails` 后，才允许生成 `proposed_operation`。

后台监控是系统固化行为。策略生效后，系统根据 `generationMeta.playbook.rules` 自动扫描数据面和组合风险条件。消息面上下文由消息脉络和组合哨兵按窗口读取，不写入策略预筛配置。

第一版不新增独立策略草案表。策略草案复用现有策略对象：`status=draft`、`source=agent`、`generationMeta.source=strategy_generation`。Agent 运行过程复用 `AgentRun` 和 `DecisionLedger`。

## 3. 与 StockPulse 的取舍

StockPulse 的持仓操作策略 workflow 有几件事值得保留：

- 先构造上下文，再让 Agent 判断。
- 保留多头、空头、风险、组合约束等不同视角。
- 最终输出结构化策略，而不是只输出长文本。
- 产物必须能进入后续监控。
- 每次 Agent 判断必须留痕，能回看 Prompt、输入、输出、结构化结果和运行过程。

当前 StockV2 不直接照搬 StockPulse 的重流水线。

第一阶段采用更轻的实现：

```text
ContextBuilder
-> 单次 StrategyGeneration Agent
-> 结构化输出校验
-> draft Strategy
-> 用户确认
```

Agent 在一次输出中给出多头理由、空头理由、关键分歧、组合约束、操作剧本和最终裁决。未来再升级成真实多 Agent 辩论。

## 4. 入口模式

`strategy_generation` 目标支持四种模式。当前第一版 HTTP 入口已开放 `manual_target`、`single_instrument` 和 `portfolio_strategy_diagnosis`；`opportunity` 保留为机会详情入口接入后的模式，不在本轮强行开放。

| 模式 | 入口 | 是否必须绑定组合 | 主要产物 |
|---|---|---:|---|
| `manual_target` | 用户输入股票和目标，例如“302132 中期看多” | 否 | 新策略草案 |
| `opportunity` | 从机会、主题、消息事件进入 | 否 | 机会策略草案 |
| `single_instrument` | 从股票详情进入 | 否 | 单股票策略草案 |
| `portfolio_strategy_diagnosis` | 从组合/仓位页进入“诊断当前组合” | 是 | 持仓处置建议 + 新策略/策略补丁 |

账户无关模式只能输出 `trade_signal`、`strategy_draft` 和监控条件。

账户绑定模式可以额外读取组合快照、成本、现金、可卖数量、风险偏好和已有策略覆盖情况，但仍不能直接生成已确认操作。

## 5. 输入模型

建议输入对象为 `StrategyGenerationInput`。当前 HTTP API 为 `POST /api/stockv2/agent/strategy-generation/run`，请求体按 Go/TypeScript API 约定使用 camelCase；Agent MCP 回填使用 `strategy-generation-report/v2`。

```json
{
  "schemaVersion": "strategy-generation-input/v1",
  "mode": "manual_target|single_instrument|portfolio_strategy_diagnosis",
  "userGoal": "用户目标、问题或备注；后端兼容 userIntent 作为兜底",
  "userIntent": "用户输入的自然语言意图",
  "targetInstruments": [
    {
      "symbol": "302132",
      "market": "SZ",
      "name": "中航成飞",
      "userNote": "可选。用户对该标的的补充说明。"
    }
  ],
  "opportunityId": "",
  "portfolioId": "",
  "timeHorizon": "short|swing|medium|long|unspecified",
  "allowedActions": ["observe", "build_position", "add_position", "hold", "reduce_position", "exit_position"],
  "evidenceScope": {
    "quote": true,
    "dailyBars": true,
    "stockProfile": true,
    "recentNews": true,
    "existingStrategy": true,
    "portfolioSnapshot": true,
    "memory": true
  }
}
```

`portfolio_strategy_diagnosis` 模式下，系统自动补齐：

- 当前持仓、成本、数量、可卖数量。
- 当前价、市值、浮盈亏、仓位占比。
- 可用现金、总资产、风险偏好、单票上限。
- 已有策略覆盖情况。
- 最近 Review、操作记录和相关记忆。

组合持仓、成本和风险约束属于用户上下文，只进入本次 `StrategyGenerationContext`，不写入通用股票画像。

## 6. Context Pack

策略生成使用新的 `StrategyGenerationContext`。它不是现有 `OperationReview` 的 `AgentContextPack` 直接复用版，因为现有 `AgentContextPack` 从 `MonitorHit` 出发；策略生成可能从用户输入、股票、机会或组合出发。

`StrategyGenerationContext` 至少包含：

- 任务模式、用户意图和目标股票。
- 股票主数据、股票画像、行业/板块/概念标签。
- 最新行情、日 K 摘要、成交量和数据新鲜度。
- 近期标准化消息、消息关联候选和来源质量。
- 已有策略版本和策略剧本。
- 组合快照、持仓成本、现金、可卖数量和风险偏好。
- 最近 Review、操作记录和相关记忆。
- 数据缺口、过期数据、未核验事实。

`StrategyGenerationContext` 可以复用现有行情、日 K、分钟线、股票画像、组合快照和策略版本的小型构造函数，但不要强行要求先生成 `MonitorHit`。

Context 必须把“事实”和“Agent 推断”分开，避免 Agent 把未核验内容包装成事实。

## 7. Agent 输出

建议输出对象为 `strategy-generation-report/v2`。

```json
{
  "schema_version": "strategy-generation-report/v2",
  "run_summary": {
    "mode": "portfolio_strategy_diagnosis",
    "overall_conclusion": "组合层面的总体结论",
    "key_conflicts": ["多空或风险审查中的主要分歧"],
    "data_quality_notes": ["数据缺口、过期或无法核验的内容"]
  },
  "drafts": [
    {
      "symbol": "302132",
      "market": "SZ",
      "name": "中航成飞",
      "draft_type": "new_strategy|strategy_patch|no_change",
      "strategy_bias": "bullish|bearish|neutral|watch",
      "thesis": "策略核心逻辑",
      "confidence": 0.72,
      "evidence_summary": ["关键证据"],
      "risk_summary": ["关键风险"],
      "invalid_conditions": ["使策略失效的条件"],
      "horizon_outlooks": [
        {
          "horizon": "short",
          "tradingDays": 5,
          "asOfPrice": 68.5,
          "direction": "bullish",
          "probabilityUp": 0.63,
          "probabilityOutperform": 0.57,
          "expectedPrice": 71.2,
          "expectedReturnPct": 3.94,
          "rangeLow": 64,
          "rangeHigh": 75,
          "targetPrice": 73,
          "targetProbability": 0.38,
          "downsideRiskPct": 7,
          "confidence": 0.65,
          "thesis": "模型综合行情、资金、基本面、消息和市场状态后的条件判断",
          "invalidConditions": ["放量跌破关键风险位"],
          "uncertainties": [],
          "dataQuality": "healthy"
        }
      ],
      "playbook": {
        "version": "v1",
        "rules": [
          {
            "id": "breakout_add",
            "action": "observe|build_position|add_position|hold|reduce_position|exit_position",
            "title": "动作标题",
            "trigger": "触发描述",
            "preconditions": "前置条件",
            "target": "触发后希望达到的状态",
            "risk": "风险备注",
            "horizon": "short|medium|long|cross_horizon",
            "forecast_basis": "该动作与三周期预测的对应依据",
            "dataPrefilters": [],
            "portfolioPrefilters": [],
            "priority": 1
          }
        ]
      },
      "portfolio_aware_suggestion": {
        "trade_signal": "buy|sell|hold|observe|build_position|add_position|reduce_position|exit_position",
        "target_position_hint": "账户绑定时可给目标仓位区间，不给最终操作单",
        "review_request": "需要立即处理时，说明应创建 OperationReview 的原因"
      }
    }
  ]
}
```

上例为避免重复只展开 short 项；真实 v2 draft 必须同时给出同结构的 medium/20 和 long/60 项。

`final_action` 这类单字段不能替代操作剧本。一只股票的策略可以同时包含建仓、加仓、持有、减仓和清仓条件。

每个 draft 都必须包含 short/5、medium/20、long/60 三个模型预测。组合评审步骤负责形成预测，最终格式化步骤原样传递；不能把候选评分、技术指标或确定性门分数直接换算成概率。策略规则必须用 `horizon` 和 `forecast_basis` 说明其对应的预测周期，跨周期冲突应形成一组协调动作，而不是三个互相矛盾的结论。

`playbook` 必须按当前代码协议写入 `generationMeta.playbook.rules`。字段命名应使用 `action`、`trigger`、`preconditions`、`target`、`risk`、`horizon`、`forecast_basis`、`dataPrefilters`、`portfolioPrefilters`，动作枚举应使用 `add_position`、`reduce_position`、`exit_position`，不要再引入 `actions/action_type/add/reduce/clear` 第二套协议。

## 8. 组合持仓诊断模式

组合持仓诊断是策略生成的特化模式：

```text
strategy_generation.mode = portfolio_strategy_diagnosis
```

它回答的问题是：

> 我现在已经持有这些股票，后续应该继续持有、加仓、减仓、清仓、等待，还是补一条正式策略？

逐持仓输出：

- 当前状态：继续持有、观察、加仓候选、减仓候选、清仓风险。
- 策略覆盖：已有、缺失、过期、需要 patch。
- 建议处理：生成新策略、更新已有策略、进入 Review、继续观察。
- 触发条件：价格、日 K、成交量、消息面、组合风险。
- 风险说明：证据不足、数据过期、集中度、波动和流动性问题。

组合层面输出：

- 总仓位和现金状态。
- 单票集中度。
- 同主题或同行业暴露。
- 优先处理的持仓。
- 需要补齐或更新的策略列表。

如果 Agent 判断需要立即处理某个持仓，系统应创建 `OperationReview`，而不是直接创建操作记录。

## 9. 确认与落库

新策略：

```text
AgentRun(strategy_generation)
-> stockv2_strategies(status=draft, source=agent)
-> 用户确认激活
-> stockv2_strategies(status=active)
-> active StrategyVersion
-> generationMeta.playbook.rules
```

更新已有策略：

```text
StrategyPatch
-> pending_acceptance
-> accepted
-> new StrategyVersion
```

账户相关操作：

```text
trade_signal
-> OperationReview
-> Guardrails
-> ProposedOperation
-> 用户确认
-> OperationRecord
```

Agent 不直接写正式策略，不直接改持仓，不绕过 Guardrails。

## 10. 与后台监控的关系

策略确认后，后台监控自动消费 `generationMeta.playbook.rules`：

```text
generationMeta.playbook.rules
-> MatchRule / 内部预筛
-> MonitorHit
-> Agent doublecheck / OperationReview
-> Alert / TradeSignal / ProposedOperation
```

当前已实现的数据面监控消费 `dataPrefilters` 和 `portfolioPrefilters`。消息关联候选不触发独立逐条监控，由消息脉络和组合哨兵在各自窗口内消费。

StockPulse 里的 `monitoring_task_blueprint` 在当前 V2 中对应 `generationMeta.playbook.rules`，不是用户手动创建的盯盘任务。

## 11. Agent 治理与留痕

`strategy_generation` 是一个独立 Agent Task。

它需要：

- 可以在 Agent 治理中绑定模型。
- 通过 Codex CLI 执行。
- 通过 MCP `submit_result(taskID)` 回填结构化结果。
- 主程序校验输出 schema 后再落库。
- 写入 `AgentRun` 和 `DecisionLedger`。
- 在运行详情里能看到输入摘要、Prompt、stdout/stderr、MCP 回填结果、结构化输出和运行子图。

第一阶段可以不做真实多 Agent 辩论，但输出中必须保留“多头理由 / 空头理由 / 风险约束 / 组合裁决”四类结构化段落，方便未来升级。

## 12. 前端入口

建议入口保持少而清晰：

- 策略页：`生成策略`。
- 股票详情：`为该股票生成策略`。
- 机会详情：`从机会生成策略`。
- 组合页：`诊断当前组合`。

生成结果进入草案列表。草案详情用 Drawer 展示：

- 总体结论。
- 每只股票的策略草案。
- 操作剧本。
- 证据与风险。
- 数据质量。
- Agent 留痕入口。
- `确认为策略`、`作为策略补丁确认`、`忽略`、`进入 Review`。

## 13. 开发顺序

建议最小开发顺序：

1. 让 `strategy_generation` Agent Task 从未来任务变成可配置、可执行。
2. 增加 `StrategyGenerationContext` builder，并复用 `AgentRun` / `DecisionLedger` 记录运行。
3. 实现人工目标生成策略，输出 `stockv2_strategies(status=draft, source=agent)`。
4. 实现组合持仓诊断模式 `portfolio_strategy_diagnosis`。
5. 用户确认草案后激活策略，保留 `StrategyVersion` 和 `generationMeta.playbook.rules`。
6. 接入已实现的数据面/组合监控，确认生效策略可产生 `MonitorHit`。
7. 将需要立即处理的账户绑定建议转入 `OperationReview`，由 Review 生成 `proposed_operation` 或 `strategy_patch`。

暂不优先做：

- 真实多 Agent 辩论。
- 复杂仓位求解器。
- 自动下单。
- 单独的用户手动盯盘创建流程。

## 14. 验收标准

基础验收：

- 用户可以从策略页输入目标，生成策略草案。
- 用户可以从组合页诊断当前组合，看到逐持仓后续建议。
- 草案确认后能生成正式策略版本和操作剧本。
- 生效策略能被后台监控消费。
- 账户无关策略不会生成具体数量、金额或目标仓位操作单。
- 账户绑定建议必须进入 Review 和 Guardrails。
- 每次 Agent 策略生成都有 Decision Ledger。

关键体验验收：

- 用户不需要手动配置盯盘规则。
- 用户能看懂“为什么生成这个策略”。
- 用户能区分策略草案、正式策略、策略补丁和操作提案。
- 用户能从运行详情回看 Agent 使用了哪些数据和上下文。
