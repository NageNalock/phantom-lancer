# happy 项目技术参考建议

文档日期：2026-06-04  
参考对象：[slopus/happy](https://github.com/slopus/happy)  
适用范围：后续设计 Codex 会话、任务执行、事件流、审批、文件/命令能力和执行器生命周期时的参考材料。本文不是实现规范，也不是路线图承诺；具体方案仍应优先服从本项目的产品定位、Go 后端、SQLite、SSE、个人单机部署和权限边界。

## 1. 参考边界

本项目与 happy 的定位不同。happy 更偏向多端远程控制与同步中继，本项目更偏向个人服务器 Web 控制台和受控运维入口。

参考时应排除：

- Claude 相关能力。
- 移动端 App 形态。
- 云中继、多端社交、多用户同步等超出个人控制台边界的能力。
- 为横向扩展引入的 Postgres、Redis、Socket.IO 集群适配等复杂度。

可以参考的是其在 agent 会话、事件抽象、审批状态、断线恢复和本地执行进程管理上的设计思路。

## 2. 总体参考原则

后续功能设计可以优先考虑这些方向：

- 保持后端是唯一执行入口，前端只提交结构化操作。
- 将 agent 原始输出转换为本项目稳定的事件模型，避免前端直接依赖 Codex 原始事件。
- 区分可恢复的持久事件和只表示在线状态的临时事件。
- 将审批、权限、路径边界和审计设计为执行链路的一部分，而不是 UI 附加功能。
- 对长任务、会话和执行进程保留可恢复、可中断、可清理的生命周期记录。
- 只有在当前 Go + SQLite + SSE 模型明显不够时，才考虑引入更复杂的实时通信或存储组件。

## 3. Codex 长会话接入

happy 对 Codex 的主要参考价值在于使用 `codex app-server` 作为长会话能力，而不是只依赖一次性 `codex exec`。

后续设计 Codex 会话时，可以考虑：

- 将一次性任务和长期会话分成两个能力面。
- 长期会话保存 Codex thread id、工作区、权限模式、当前状态和最近活动时间。
- 每个用户输入作为一个 turn 处理，turn 可以有开始、输出、工具调用、审批、完成、失败、取消等状态。
- 模型、sandbox、审批策略等参数尽量按 turn 生效，而不是要求重启整个会话。
- 中断操作应优先走 Codex 自身 interrupt 能力；如果进程无响应，再进入更强的清理路径。

这些思路可以作为未来实现 `codex app-server` 模块时的参考，不要求 MVP 立即实现。

## 4. 统一事件模型

happy 将不同 agent 的输出映射成统一 session event，这是值得参考的方向。我们也可以为本项目定义一个更贴合 Web 终端的事件层。

可以考虑的事件类别：

- `turn.started` / `turn.completed` / `turn.failed` / `turn.cancelled`
- `message.text`
- `message.reasoning`
- `tool.started` / `tool.completed`
- `command.started` / `command.completed`
- `patch.started` / `patch.completed`
- `approval.requested` / `approval.resolved`
- `job.ready` / `job.interrupted`

重点不是照搬字段名，而是让前端面向稳定语义渲染。Codex 原始事件、CLI stdout、stderr、后端内部状态都可以在后端归一化后再进入事件表和 SSE。

## 5. 持久事件与临时状态

happy 将可恢复更新和临时在线状态分开。这个设计适合本项目后续演进。

建议后续区分：

- 持久事件：任务输出、会话消息、审批请求、审批结果、文件变更、审计记录。
- 临时状态：会话是否正在运行、是否 thinking、执行器是否在线、前端连接是否断开。

持久事件应可通过 `after` 或 sequence 补拉。临时状态可以通过 SSE heartbeat、状态接口或内存状态提供，不一定都写入审计。

## 6. 审批与权限状态

happy 的审批处理值得参考：审批请求进入可观察状态，用户决策再回传给 agent 执行层。

本项目后续设计审批时可以考虑：

- 审批请求有独立实体，记录 workspace、操作类型、请求参数摘要、创建时间和过期状态。
- 执行器等待审批结果时，前端可以刷新、断线重连并继续看到同一个 pending request。
- 审批结果进入审计，且能和对应 job、turn、tool call 关联。
- 默认失败策略应保守。审批处理异常、连接断开或超时后，不应默认放行。

审批 UI 不应只是弹窗，也应能在“审批中心”和任务详情中恢复查看。

## 7. 受控文件、搜索和命令能力

happy 没有把所有能力都设计成裸 shell，而是提供 read file、write file、list directory、ripgrep、diff 等受控操作。这与本项目的权限边界方向一致。

后续实现文件、日志、搜索、diff 或服务操作时，可以考虑：

- 每类操作都有明确 API 和参数 schema。
- 路径必须先经过 workspace 或资源白名单校验。
- 写文件可考虑 expected hash 或 revision，避免覆盖用户刚刚修改的内容。
- 搜索、日志读取、diff 生成等操作设置超时、输出上限和审计摘要。
- 任意 shell 作为受限能力处理，不作为普通能力的默认实现方式。

## 8. Runner 和执行器生命周期

happy 的 daemon/runner 设计对我们未来支持长任务和多执行器有参考价值。当前项目可以先保持单进程 Go 服务，但长期可以抽象出 runner 生命周期。

可考虑的生命周期信息：

- runner id、进程 id、启动来源、工作目录。
- 当前 job/session、启动时间、最近心跳。
- 是否可中断、是否已退出、退出码或错误原因。
- 服务重启后如何标记未完成任务。
- 对外暴露的健康状态和最近日志摘要。

这个抽象可以先在 Go 进程内部实现，后续如果支持多服务器或独立 agent worker，再扩展为远程执行器。

## 9. 断线恢复与幂等

happy 使用 sequence、local id 和补拉接口处理断线和重复提交。本项目已经有按 scope 的事件 sequence，可以继续强化。

后续可考虑：

- 前端连接 SSE 前先拉取历史，再订阅实时流。
- SSE 断线后使用最后 sequence 补拉缺失事件。
- 用户提交消息或操作请求时带 client request id，用于避免刷新或重试造成重复执行。
- 长会话消息和 job 事件分页读取，避免大任务一次性加载全部历史。

这些能力对移动端不是必需条件，对普通 Web 刷新和网络波动同样有价值。

## 10. Schema 与协议演进

happy 将 wire schema 抽成共享包，核心价值是减少协议漂移。我们不需要照搬 TypeScript 包形态，但可以借鉴协议显式化的做法。

后续可以考虑：

- 为 API response、SSE event、approval request 定义清晰 schema。
- 前后端共享类型可以通过 OpenAPI、JSON Schema、代码生成或小型手写类型文件实现。
- 新事件尽量 additive，避免破坏旧前端。
- 重要协议变更写入文档，并配少量兼容性测试。

## 11. 不建议照搬的部分

以下内容不建议作为当前阶段目标：

- 全量端到端加密导致服务端无法理解事件内容。我们的服务端需要做权限判断、策略匹配和审计，不能把所有执行上下文都变成 opaque blob。
- Socket.IO + Redis streams 的多副本实时同步。当前个人单机部署使用 SSE 更简单。
- Postgres/Prisma 数据层。SQLite 更符合当前部署目标。
- 移动端特化的 push、设备切换和 App 状态逻辑。
- 过早引入独立 daemon。只有当 Go 单进程无法可靠管理长任务或多执行器时，再拆分。

## 12. 使用方式

后续设计相关功能时，可以把本文作为检查清单：

- 是否需要长会话，而不是一次性任务。
- 是否需要稳定事件模型，而不是暴露原始输出。
- 是否需要审批可恢复。
- 是否需要断线补拉和幂等。
- 是否可以用受控操作替代 shell。
- 是否需要记录 runner 生命周期。

如果某项建议会显著增加 MVP 复杂度，应优先选择更小的实现，并在数据模型和事件命名上保留后续扩展空间。

## 13. 参考资料

- [slopus/happy README](https://github.com/slopus/happy/blob/main/README.md)
- [happy protocol](https://github.com/slopus/happy/blob/main/docs/protocol.md)
- [happy session protocol](https://github.com/slopus/happy/blob/main/docs/session-protocol.md)
- [happy Codex app-server migration notes](https://github.com/slopus/happy/blob/main/docs/plans/codex-app-server-migration.md)
- [happy wire package notes](https://github.com/slopus/happy/blob/main/docs/happy-wire.md)
