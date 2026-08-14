# StockV2 Agent 完整上下文归档

## 目标与范围

StockV2 可以按任务绑定选择性地把一次 `AgentRun` 的完整脱敏上下文流式归档到共享 S3 兼容对象存储，供离线 AI 复盘。首版覆盖：

- `operation_review`
- `strategy_generation`
- `opportunity_discovery`
- `portfolio_sentinel`

`news_event_review` 和 `stock_profile_summary` 不归档，避免高频批处理造成无界资源增长。

归档是旁路能力。它不参与 Agent 成功判定，不写归档索引表，不在服务器落本地副本，也不提供读取 API、MCP 或前端浏览器。

## 配置

每个受支持任务在“Agent -> 模型与任务 -> 任务绑定 -> 高级设置”暴露：

- 是否归档完整 Agent 上下文，默认关闭。
- 共享对象存储 Profile。

启用时 Profile 必须存在并包含 Bucket、Endpoint 和凭据。删除仍被任务归档配置引用的 Profile 会被拒绝。

Bucket 应保持私有，并在对象存储侧启用默认加密和未完成 multipart upload 生命周期清理。仓库、数据库和日志均不保存对象存储凭据副本。

## 写入与资源边界

一次 `AgentRun` 对应一个 `trace-v1.jsonl.gz` 对象。事件先进入最多 8 MiB 的进程内字节队列，再通过 `gzip.BestSpeed` 和 `io.Pipe` 直接写入 S3 multipart upload；part 为 8 MiB，并发为 1。

任务结束只关闭 recorder，不等待网络上传完成。序列化失败、队列溢出、初始化失败或上传失败会取消 multipart 并整体放弃该归档，Agent 结果继续按原路径持久化。服务崩溃、机器重启和网络中断不做本地恢复。

单条脱敏事件超过 1 MiB 时，以带 SHA-256 的 `artifact_start`、`artifact_chunk`、`artifact_end` 分片表示。归档记录单调序号，正常终态带 prior-events SHA-256。

## 内容与版本

归档包含 manifest、精确序列化输入、模型请求/响应、Provider 或 CLI 实际暴露的 reasoning、工具调用/结果、多步骤状态、服务端校验、模型提交结果、最终应用结果和成功/失败终态。系统不会生成或推断 Provider 未暴露的私有思考过程。

对象路径同时包含管线名、独立管线修订号、逻辑操作 ID、AgentRun ID、attempt 和格式版本。首版四条管线均从 `r0001` 开始。主模型和备模型各有独立对象，通过相同 `logicalOperationId`、备模型的 `parentRunId` 与 `attempt=2` 关联。

详细离线解析规范只维护在 `.agents/skills/stockv2-analysis-trace-format/references/trace-format-v1.md`。该 Skill 禁止隐式调用，只供用户明确要求的历史归档分析使用，不得进入日常开发、调试或运行时投资 Agent 上下文。
