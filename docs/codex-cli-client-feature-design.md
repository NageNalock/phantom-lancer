# Codex CLI Client 模块功能设计

文档日期：2026-06-07
关联文档：

- [personal-web-terminal-product-features.md](./personal-web-terminal-product-features.md)
- [personal-web-terminal-technical-design.md](./personal-web-terminal-technical-design.md)
- [happy-technical-reference.md](./happy-technical-reference.md)
- [codex-openai-gateway-feature-design.md](./codex-openai-gateway-feature-design.md)

参考来源：

- [Codex CLI command reference](https://developers.openai.com/codex/cli/reference)
- [Codex CLI features](https://developers.openai.com/codex/cli/features)
- [Codex App Server](https://developers.openai.com/codex/app-server)
- [Codex non-interactive mode](https://developers.openai.com/codex/noninteractive)
- [Agent approvals and security](https://developers.openai.com/codex/agent-approvals-security)
- [AGENTS.md instructions](https://developers.openai.com/codex/guides/agents-md)

## 1. Design Read

Reading this as: 个人服务器控制台里的 Codex 工作台，面向单 owner 技术用户，采用 Quiet Agent Workbench / Quiet DevOps Control Plane 语言，强调工作区上下文、会话恢复、审批可见、低噪音事件流和受控权限边界。

本模块不是营销页，也不是在 Web 里重写一个完整 IDE。它的目标是在部署机已安装 `codex` CLI 时，由 Phantom Lancer 作为受控 Web 外壳，提供接近 Codex 桌面客户端的会话、工作区、审批、事件流、恢复和诊断体验。

## 2. 产品定位

`Codex CLI Client` 是独立一级能力域，显示名建议为 `Codex`。它和 `Codex Gateway` 并列，而不是上下级关系。

- `Codex`：面向 owner 在 Phantom Lancer Web 控制台内使用部署机本地 `codex` CLI。会话绑定 workspace、权限模式、事件流和审批。
- `Codex Gateway`：面向外部 OpenAI 兼容客户端，通过 `/v1/*` HTTP API 调用 Codex/OpenAI 上游能力，不绑定 workspace，不执行 shell，不修改文件。

旧版 Codex 客户端代码已经移除，不能复用旧 API、旧前端状态或旧 runner 假设。数据库中可能仍有旧表或旧事件数据；新版必须使用新的表名前缀和迁移策略，避免和旧残留发生语义冲突。

## 3. 目标和非目标

### 3.1 目标

- 探测部署机是否安装 `codex` CLI、版本是否可用、认证是否完成、sandbox 依赖是否健康。
- 提供接近 Codex 桌面客户端的信息架构：新对话、搜索、项目/工作区、最近会话、置顶会话、模型和权限选择。
- 支持在受控 workspace 内创建、恢复、继续、中断和归档 Codex 会话。
- 将 Codex 原始输出归一化为 Phantom Lancer 稳定事件，通过 SSE 实时展示并支持刷新后恢复。
- 支持审批请求可恢复：Codex 需要越权、联网或执行敏感操作时，Web UI 可以展示、允许一次、拒绝或中断。
- 支持只读咨询、workspace write 自动模式和受控审批模式。
- 对命令、路径、网络、secret、stdout/stderr 做外层安全约束、脱敏和审计。
- 在 `codex app-server` 不可用或协议不兼容时，降级到 `codex exec --json` 的一次性任务能力。

### 3.2 非目标

- 不安装或自动升级 `codex` CLI。安装由 owner 在服务器上完成，Phantom Lancer 只探测和使用。
- 不读取、展示、导出或托管 `~/.codex/auth.json`、access token、refresh token 或 ChatGPT cookie。
- 不绕过 Codex 自身 sandbox、approval、AGENTS.md、MCP、skills、rules 和 config 机制。
- 不使用 `--dangerously-bypass-approvals-and-sandbox`、`--yolo` 或默认 full access 作为产品路径。
- 不把 Codex 会话公开成 OpenAI-compatible `/v1/*` API；该能力属于 `Codex Gateway`。
- 不在 MVP 内实现多用户协作、多人共享会话、云任务编排、完整 GitHub PR 工作流或桌面 GUI 控制。
- 不直接提供任意 shell 终端；所有 shell 能力必须由 Codex sandbox 和 Phantom Lancer 外层策略共同约束。

## 4. 功能范围

### 4.1 CLI 探测和健康状态

页面进入 `Codex` 时先展示本机能力状态：

- `codex` binary 是否存在。
- version、PATH 来源、运行用户摘要。
- `codex doctor --json` 或可用诊断命令的结果摘要。
- auth 状态：只显示已登录、未登录、不可判定，不显示 token。
- sandbox 状态：Linux 上 `bubblewrap` 或相关 user namespace 能力是否可用。
- app-server 能力：`codex app-server` 是否可启动，stdio transport 是否可用。
- app-server 运行状态：主程序定时检查 managed app-server 是否 running、ready、stopped、failed 或 degraded。
- exec 能力：`codex exec --json` 是否可运行。
- 模型目录：优先通过 CLI 支持的模型/doctor/debug 能力探测；不可用时只显示用户配置或最近使用，不硬编码最新模型列表。

状态分级：

- `ready`：可以创建会话。
- `degraded`：只能使用 `exec` 或只读模式。
- `needs_setup`：未安装、未登录或 sandbox 依赖不可用。
- `unavailable`：binary 无法执行或版本不支持所需能力。

主程序必须有后台探测循环：

- 服务启动后立即探测一次。
- 默认每 15-30 秒检查 app-server runtime 状态，具体间隔进入 Codex 模块设置。
- 检查项包括进程是否存活、stdio transport 是否可用、JSON-RPC initialize 是否已完成、最近一次 heartbeat 或 probe 时间。
- 探测失败只记录脱敏错误摘要，不写完整 stderr 或环境变量。

### 4.2 工作区管理

Codex 会话必须绑定 workspace。workspace 不是通用设置项，而是 Codex 模块内的核心对象。

功能：

- 从全局允许根目录下选择或登记 workspace。
- 展示 label、路径摘要、Git 分支、最近活动、信任状态和默认权限模式。
- 支持工作区级默认模型、sandbox、approval policy 和网络策略。
- 支持最近使用和置顶。
- 不允许登记允许根目录之外的路径。

信任状态：

- `untrusted`：默认 read-only，只允许咨询和代码阅读。
- `trusted`：允许 `workspace-write + on-request`。
- `restricted`：路径可读，但禁止写入和网络。

### 4.3 会话列表和搜索

左侧上下文列应接近 Codex 桌面客户端的任务组织方式，但保持个人服务器控制台的低噪音风格。

功能：

- 新对话入口。
- 搜索历史会话标题、摘要、workspace、模型和错误摘要。
- 置顶会话。
- 按项目/workspace 分组的最近会话。
- 会话状态：idle、running、needs_approval、failed、archived。
- 支持归档、重命名、置顶/取消置顶。

搜索默认只查本地 SQLite 中的会话摘要、用户消息摘要、agent 消息摘要和错误摘要；不扫描服务器文件系统。

### 4.4 会话工作区

主工作区围绕当前 thread 展开：

- 空状态：居中的轻量 composer，不做大 hero 或营销式欢迎区。
- 会话中：消息流、计划、命令、diff、审批、错误和最终响应。
- 底部 composer：输入 prompt、选择 workspace、权限模式、模型和可选附件。
- 右侧 inspector：当前 workspace、Git 状态摘要、CLI 状态、活动 turn、审批、最近错误和事件诊断。

消息与事件类型：

- `thread.started` / `thread.resumed` / `thread.archived`
- `turn.started` / `turn.completed` / `turn.failed` / `turn.cancelled`
- `message.user` / `message.agent` / `message.reasoning`
- `command.started` / `command.completed`
- `file_change.started` / `file_change.completed`
- `approval.requested` / `approval.resolved`
- `tool.started` / `tool.completed`
- `diagnostic.warning` / `diagnostic.error`

前端只面向这些稳定事件渲染，不直接依赖 Codex 原始 JSON-RPC method 或 JSONL event 字段。

### 4.5 权限和审批

权限选择器放在 Codex composer 下方或旁侧，属于 Codex 模块内部控制，不进入通用 `设置` 页面。

MVP 支持：

- `Read-only`：`--sandbox read-only --ask-for-approval on-request`。
- `Workspace write`：`--sandbox workspace-write --ask-for-approval on-request`。
- `Auto review`：仅当本机 Codex 配置和版本支持时展示；否则隐藏。

默认策略：

- 未信任 workspace 默认 read-only。
- 信任且 Git 管理的 workspace 可默认 workspace-write + on-request。
- 网络访问默认关闭。需要联网的操作必须走审批或显式 workspace 策略。
- 不提供 full access 快捷按钮。后续如果支持，必须放在高级设置中，并要求 owner 二次确认和 audit。

审批请求必须可恢复：

- 待审批请求写入 `codex_cli_approvals`。
- 前端刷新后仍能看到 pending request。
- 允许一次、拒绝和中断都写入 audit。
- 审批超时或后端重启后默认失败关闭，不自动放行。

### 4.6 附件和图片输入

MVP 可支持上传图片作为 Codex 输入，但必须受控：

- 上传文件保存到 Phantom Lancer 受控临时目录。
- 文件大小、类型和数量有限制。
- 传给 CLI 时只传本地临时文件路径。
- 不把图片 base64、data URL 或完整远程图片 URL 写入日志、audit 或服务日志。
- turn 完成后按 retention 清理临时附件。

### 4.7 诊断和设置

Codex 模块自己的设置只放在 `Codex > Settings`：

- `codex` binary path：默认从 PATH 探测，可手动指定。
- `CODEX_HOME`：默认使用运行用户的 Codex home，可选择专用目录。
- 默认 sandbox 和 approval policy。
- 默认模型，仅作为 CLI 参数；实际可用性以运行时探测为准。
- app-server 启用开关。
- app-server 定时检查间隔。
- app-server 是否随 Phantom Lancer 启动后自动启动；默认建议关闭，由 owner 点击启动。
- exec fallback 启用开关。
- 事件保留策略。

通用 `设置` 只保留跨模块项，例如允许根目录、全局 Cookie 策略、全局认证和服务运行参数。

### 4.8 app-server 状态和一键启动

页面必须展示 app-server 的当前状态，但保持低噪音：

- `running`：显示绿色小状态点、PID 摘要、已运行时长和最近 probe 时间。
- `starting`：显示 loading 状态，启动按钮 disabled。
- `stopped`：显示中性状态和 `Start app-server` 按钮。
- `failed`：显示橙色或红色状态、最近失败摘要和 `Retry start` 按钮。
- `degraded`：显示 warning，说明只能使用 exec fallback 或只读历史。

一键启动行为：

1. Owner 点击 `Start app-server`。
2. 前端调用 `POST /api/codex/app-server/start`，必须带 owner session 和 CSRF。
3. 后端确认 CLI installed、auth 可用、sandbox 依赖满足、没有已运行 app-server。
4. 后端用 env allowlist 启动 `codex app-server --listen stdio://`。
5. 后端完成 initialize handshake 后将状态更新为 `running`。
6. 页面通过轮询状态接口或 SSE runtime event 更新按钮状态。

约束：

- 不从浏览器直接启动进程。
- 不把启动按钮做成全屏欢迎区或大 CTA；应放在 Codex inspector、Diagnostics row 或空状态 composer 附近。
- 启动失败只展示错误摘要和排查入口，不展示完整 stderr、token、环境变量或绝对 secret 路径。
- MVP 不做自动无限重启；自动重启如果后续支持，必须有次数上限、backoff 和 audit。

## 5. 接入模式

### 5.1 首选：`codex app-server`

当本机 CLI 支持时，优先使用受主程序管理的内部 app-server runtime：

```bash
codex app-server --listen stdio://
```

后端通过 stdio 发送 JSON-RPC 消息并读取通知。MVP 不暴露非 loopback WebSocket listener，也不把 app-server 直接开放给浏览器。主程序负责定时检查该 runtime 状态；未启动时，页面提供一键启动按钮。

核心流程：

1. `CodexAppServerSupervisor` 在服务启动后登记 runtime 状态并启动定时 probe。
2. Owner 点击页面按钮或设置允许启动时，后端启动 app-server 子进程，使用受控 env allowlist。
3. 后端发送 `initialize` 和 `initialized`，成功后状态变为 `running`。
4. 新会话调用 `thread/start`，恢复会话调用 `thread/resume`。
5. 每次用户输入调用 `turn/start`，活动 turn 内的补充输入可映射为 `turn/steer`。
6. 后端读取 app-server notifications，转换为 Phantom Lancer 事件。
7. `turn/completed`、`turn/failed` 或 `turn/interrupt` 后更新 turn 状态。

app-server 属于 Codex 的深度集成接口，部分 transport 和字段仍可能演进。实现必须做能力探测、版本记录和 fallback，不把某个版本的 schema 当成永久稳定协议。

### 5.2 降级：`codex exec --json`

当 app-server 不可用但 `codex exec --json` 可用时，提供一次性任务模式：

```bash
codex exec --json --sandbox read-only "Summarize this repository"
```

适用：

- 代码阅读、总结、诊断。
- 简单自动化任务。
- app-server 协议不兼容时的兜底。

限制：

- 不提供完整桌面客户端式长会话体验。
- 审批能力受 `codex exec` 非交互模式限制；默认只允许 read-only。需要写入的 exec fallback 必须由 owner 显式选择低风险策略，无法交互审批时默认拒绝。
- 不能把需要交互审批的操作默默转成 full access。

### 5.3 不采用的接入方式

- 不从浏览器直接连接 `codex app-server` WebSocket。
- 不默认启动 `codex app-server --listen ws://0.0.0.0:*`。
- 不使用 `codex app` 启动桌面客户端；服务器部署通常没有 GUI。
- 不解析或修改 Codex 私有 session 文件作为主协议。
- 不通过 shell 伪终端驱动 TUI 文本界面。

## 6. 后端设计

### 6.1 模块结构

建议新增 `internal/codexclient`，避免复用旧版 `internal/codex` 的语义残留。

模块职责：

- `Detector`：探测 binary、version、doctor、auth、sandbox 和 app-server/exec 能力。
- `AppServerSupervisor`：定时检查 app-server runtime，处理一键启动、停止、异常退出和状态广播。
- `Runner`：管理 turn 级执行、exec fallback 和 app-server client 调用。
- `AppServerClient`：JSON-RPC request/response、notification 读取和超时控制。
- `ExecClient`：`codex exec --json` 命令构造、JSONL 解析和退出状态处理。
- `EventMapper`：Codex 原始事件到 Phantom Lancer 稳定事件的转换。
- `ApprovalBroker`：审批请求持久化、等待、恢复和决策回传。
- `WorkspacePolicy`：允许根目录、trust state、sandbox、network 和 add-dir 校验。
- `Redactor`：prompt、stderr、URL、token、secret 和路径摘要脱敏。
- `Service`：HTTP API 编排、状态汇总、audit 和 SSE 事件写入。

### 6.2 子进程环境

子进程不能直接继承完整 Phantom Lancer 服务环境。必须显式构造 env allowlist：

- 允许：`PATH`、`HOME`、`USER`、`SHELL`、`TERM`、`CODEX_HOME`、必要 locale。
- 可配置允许：owner 明确配置给 Codex 的 provider/env。
- 禁止默认继承：Phantom Lancer session secret、database path、API key、cookie secret、对象存储 secret、V2Ray credential、任意未知 secret-like 环境变量。

工作目录：

- 必须是已登记 workspace。
- 必须落在允许根目录内。
- `--add-dir` 只能指向允许根目录内的额外目录，且必须记录 audit。

### 6.3 进程生命周期

MVP 建议由主程序管理一个内部 app-server runtime，用于当前 Phantom Lancer 实例的 Codex 会话。它不暴露给浏览器，也不绑定非 loopback WebSocket。后续如果需要隔离多个 runtime profile，再按 CODEX_HOME、workspace trust 或运行用户拆分。

生命周期状态：

- `stopped`
- `starting`
- `ready`
- `running_turn`
- `waiting_approval`
- `idle`
- `interrupting`
- `exited`
- `failed`

后端重启时：

- 先将内存 runtime 状态标记为 `unknown`，再通过 probe 判断是否需要显示 `stopped` 或 `failed`。
- 所有 `running` turn 标记为 `unknown` 或 `interrupted_by_server_restart`。
- 保留 thread 和历史事件。
- 用户重新打开 thread 时，优先通过 Codex thread id 恢复；无法恢复时进入只读历史状态。

### 6.4 API 设计

管理 API 使用 owner session + CSRF；不对外开放 public bearer API。

状态：

- `GET /api/codex/status`
- `POST /api/codex/status/probe`
- `GET /api/codex/app-server/status`
- `POST /api/codex/app-server/start`
- `POST /api/codex/app-server/stop`
- `POST /api/codex/app-server/restart`
- `GET /api/codex/settings`
- `PUT /api/codex/settings`

`start`、`stop` 和 `restart` 都必须要求 owner session + CSRF。MVP 可以只在 UI 暴露 `start` 和 `retry start`，`stop/restart` 先作为诊断 API 或后续能力。

工作区：

- `GET /api/codex/workspaces`
- `POST /api/codex/workspaces`
- `PATCH /api/codex/workspaces/{id}`
- `DELETE /api/codex/workspaces/{id}`

会话：

- `GET /api/codex/threads`
- `POST /api/codex/threads`
- `GET /api/codex/threads/{id}`
- `PATCH /api/codex/threads/{id}`
- `POST /api/codex/threads/{id}/archive`
- `POST /api/codex/threads/{id}/resume`
- `POST /api/codex/threads/{id}/fork`

turn：

- `POST /api/codex/threads/{id}/turns`
- `POST /api/codex/turns/{id}/steer`
- `POST /api/codex/turns/{id}/interrupt`
- `GET /api/codex/threads/{id}/events`

审批：

- `GET /api/codex/approvals`
- `POST /api/codex/approvals/{id}/approve`
- `POST /api/codex/approvals/{id}/deny`

附件：

- `POST /api/codex/attachments`
- `DELETE /api/codex/attachments/{id}`

事件实时推送复用现有 SSE：

- `GET /api/events/history?scope=codex.thread.{thread_id}`
- `GET /api/events/stream?scope=codex.thread.{thread_id}`

如果现有 Event API 不支持 scope 参数，应先扩展 Event Module，而不是为 Codex 单独引入 WebSocket。

## 7. 数据模型

### 7.1 兼容原则

旧版 Codex 客户端代码已删除，但 SQLite 中可能残留旧表、旧索引、旧 event scope 或旧 settings key。

迁移原则：

- 新版表统一使用 `codex_cli_` 前缀。
- 不创建裸 `codex_sessions`、`codex_messages`、`codex_events` 等容易与旧版混淆的表名。
- migration 只做 additive `CREATE TABLE IF NOT EXISTS`、`CREATE INDEX IF NOT EXISTS` 和必要字段补充。
- 不在 MVP 自动删除旧表或迁移旧数据。
- 启动时可探测旧表存在性，并在 `Codex > Settings > Diagnostics` 中显示 `legacy data detected`。
- 若未来需要导入旧数据，必须单独设计显式导入流程，并给每条导入记录标记 `legacy_imported`。
- 旧表存在不能阻止新模块启动，除非数据库损坏或新表创建失败。

### 7.2 `codex_cli_installations`

- `id`
- `binary_path`
- `version`
- `status`
- `capabilities_json`
- `doctor_summary_json`
- `last_probe_error`
- `detected_at`
- `created_at`
- `updated_at`

不记录完整 doctor 原始输出，只保存脱敏摘要和能力位。

### 7.3 `codex_cli_workspaces`

- `id`
- `label`
- `path`
- `path_summary`
- `trust_state`
- `default_model`
- `default_sandbox`
- `default_approval_policy`
- `network_policy_json`
- `last_opened_at`
- `created_at`
- `updated_at`

`path` 存储规范化后的实际路径；API response 默认返回摘要，可在详情页按需显示完整路径。

### 7.4 `codex_cli_threads`

- `id`
- `codex_thread_id`
- `workspace_id`
- `title`
- `status`
- `source_mode`
- `model`
- `sandbox_mode`
- `approval_policy`
- `pinned`
- `archived_at`
- `last_turn_id`
- `last_error`
- `created_at`
- `updated_at`

`codex_thread_id` 可以为空，表示 exec fallback 产生的一次性线程或尚未成功创建 app-server thread。

### 7.5 `codex_cli_turns`

- `id`
- `thread_id`
- `codex_turn_id`
- `status`
- `prompt_summary`
- `model`
- `sandbox_mode`
- `approval_policy`
- `started_at`
- `completed_at`
- `error_summary`
- `usage_json`
- `created_at`
- `updated_at`

`prompt_summary` 必须做长度上限和 secret redaction；完整用户输入如需恢复 UI，应作为受控事件分片保存。

### 7.6 `codex_cli_events`

- `id`
- `thread_id`
- `turn_id`
- `sequence`
- `event_type`
- `codex_method`
- `item_type`
- `payload_json`
- `text_preview`
- `created_at`

约束：

- `(thread_id, sequence)` 唯一。
- 单条 `payload_json` 有大小上限，超出时写入截断标记。
- `text_preview` 只用于列表和搜索，必须脱敏。
- 长 stdout/stderr 按事件分片进入 events，不进入服务 `slog`。

### 7.7 `codex_cli_approvals`

- `id`
- `thread_id`
- `turn_id`
- `codex_request_id`
- `status`
- `action_kind`
- `command_preview`
- `cwd_summary`
- `risk_level`
- `request_payload_json`
- `decision`
- `decided_at`
- `expires_at`
- `created_at`
- `updated_at`

不保存完整 secret、完整环境变量或完整 token-bearing URL。

### 7.8 `codex_cli_runs`

- `id`
- `thread_id`
- `turn_id`
- `mode`
- `pid`
- `status`
- `started_at`
- `last_heartbeat_at`
- `exited_at`
- `exit_code`
- `error_summary`

`mode = app_server` 时，`thread_id` 和 `turn_id` 可以为空，表示这是主程序管理的 app-server runtime；`mode = exec` 或 turn 级临时 runner 才关联具体 thread/turn。`pid` 只用于运行期诊断，不作为安全边界。

### 7.9 `codex_cli_attachments`

- `id`
- `thread_id`
- `turn_id`
- `kind`
- `filename`
- `content_type`
- `size_bytes`
- `storage_path`
- `expires_at`
- `created_at`

附件默认短期保留；删除 thread 或超过 retention 后清理本地文件。

### 7.10 Settings key

模块设置可以存入现有 settings 表，key 使用 `codex_cli.*` 前缀：

- `codex_cli.enabled`
- `codex_cli.binary_path`
- `codex_cli.codex_home`
- `codex_cli.default_model`
- `codex_cli.default_sandbox`
- `codex_cli.default_approval_policy`
- `codex_cli.app_server_enabled`
- `codex_cli.app_server_probe_interval_seconds`
- `codex_cli.app_server_start_on_launch`
- `codex_cli.exec_fallback_enabled`
- `codex_cli.event_retention_days`
- `codex_cli.max_events_per_thread`
- `codex_cli.max_event_payload_bytes`

不要复用旧版 `codex.*` settings key，避免读到残留配置。

## 8. 安全、审计和日志

### 8.1 外层安全边界

Phantom Lancer 必须在调用 CLI 前先做外层校验：

- owner session 有效。
- 写操作校验 CSRF。
- workspace 位于允许根目录内。
- sandbox 和 approval policy 符合 workspace trust state。
- network policy 默认关闭。
- 附件位于受控临时目录。
- 子进程 env 已经过 allowlist。

### 8.2 Codex 内层边界

Codex CLI 自身仍负责：

- AGENTS.md 指令发现。
- Codex sandbox 执行。
- Codex approval request。
- MCP、skills、rules、hooks 和 config.toml。
- Codex session/transcript 的本地管理。

Phantom Lancer 不能假装这些能力属于自己实现；UI 只做可观察、可配置和可恢复的 Web 管理面。

### 8.3 审计事件

建议新增 audit event：

- `codex_cli.workspace.created`
- `codex_cli.workspace.updated`
- `codex_cli.thread.created`
- `codex_cli.thread.archived`
- `codex_cli.turn.started`
- `codex_cli.turn.interrupted`
- `codex_cli.app_server.start_requested`
- `codex_cli.app_server.started`
- `codex_cli.app_server.start_failed`
- `codex_cli.app_server.stopped`
- `codex_cli.approval.requested`
- `codex_cli.approval.approved`
- `codex_cli.approval.denied`
- `codex_cli.settings.updated`
- `codex_cli.probe.failed`
- `codex_cli.legacy_data.detected`

audit payload 只记录：

- workspace id
- thread id
- turn id
- approval id
- sandbox mode
- approval policy
- status
- risk level
- error summary

不记录完整 prompt、完整 stdout/stderr、完整 diff、secret、Authorization、cookie、token、完整远程 URL query。

### 8.4 服务日志

服务 `slog` 只记录关键异常摘要：

- CLI binary 探测失败。
- app-server 启动失败或异常退出。
- app-server probe 连续失败摘要。
- JSON-RPC parse failure 摘要。
- exec 退出失败摘要。
- event 持久化失败。
- approval 回传失败。
- 清理超时和资源泄漏摘要。

不要记录：

- 每个成功事件。
- SSE heartbeat。
- 完整 prompt。
- 完整 stdout/stderr。
- 完整 Codex 原始 payload。
- token、cookie、API key。

### 8.5 生命周期和清理

- 事件按 thread 保留，默认限制最大事件数和最大保留天数。
- 长输出超过单事件上限时分片，超过 thread 总上限时提示已截断。
- 临时附件按 `expires_at` 清理。
- 后端重启后清理孤儿 app-server/exec 进程时只记录摘要。
- app-server 定时 probe 只更新 runtime 状态，不为每次成功 probe 写服务日志或 audit。
- 不删除 Codex 自己的 `CODEX_HOME` session 文件，除非后续明确做 Codex 数据管理功能。

## 9. UI 设计

### 9.1 信息架构

一级导航新增 `Codex`，与 `Codex Gateway`、`日志`、`Images`、`V2Ray`、`设置` 并列。

`Codex` 内部二级视图：

- `Threads`：会话和新对话。
- `Workspaces`：项目路径、信任状态和默认权限。
- `Approvals`：待审批和历史。
- `Diagnostics`：CLI 安装、认证、sandbox 和 app-server 状态。
- `Settings`：Codex 模块设置。

不要把 `Workspaces`、`Approvals` 或 `Diagnostics` 提升为全局一级导航。

### 9.2 页面结构

桌面端：

- 左侧全局导航。
- Codex 内部上下文列：新对话、搜索、置顶、项目分组、最近会话。
- 主工作区：当前 thread 或轻量空状态 composer。
- 右侧 inspector：workspace、Git、CLI、审批、错误和事件诊断。

移动端：

- 顶部显示当前 thread/workspace。
- 会话列表进入抽屉。
- composer 固定底部。
- 权限、模型和 workspace 选择收进 bottom sheet。
- 审批请求必须突出但不遮挡长输出阅读。

### 9.3 视觉语言

遵循 Quiet Agent Workbench / Quiet DevOps Control Plane：

- 浅色中性底、近黑文字、细边框、低对比选中态。
- 单一克制强调色用于当前焦点和主操作。
- 状态色语义固定：绿色 success/available，橙色 warning/stale/offline，红色 danger。
- 小字号、高密度、可扫描。
- 技术值使用 monospace。
- 图标使用项目统一线性图标库。
- 不使用营销 hero、渐变背景、玻璃拟态、大插画、AI 紫蓝光或装饰动效。

### 9.4 关键组件

- Composer：多行输入、发送、中断、附件、workspace selector、model selector、permission selector。
- Thread list item：标题、workspace、状态、最近活动、置顶、错误标记。
- Event stream：按语义类型折叠/展开，命令和 diff 默认紧凑展示。
- Approval panel：命令摘要、cwd、命中规则、风险、允许一次、拒绝、中断。
- Inspector：稳定窄栏，不做厚重卡片。
- Diagnostics row：安装、认证、sandbox、app-server、exec fallback。
- App-server status strip：小状态点、最近 probe、启动按钮、失败摘要。

## 10. 实施顺序

1. 确认本功能文档和总技术边界。
2. 新增 SQLite migration，创建 `codex_cli_*` 表，并加入旧表探测。
3. 新增 `internal/codexclient.Detector`，实现 binary/version/doctor 能力探测。
4. 新增 AppServerSupervisor、定时 probe、一键启动 API 和 fake app-server 测试夹具。
5. 新增 workspace store 和 path policy。
6. 新增 app-server stdio JSON-RPC client，使用 fake app-server 覆盖协议解析。
7. 新增 EventMapper 和 SSE 历史恢复。
8. 新增 thread/turn API，先支持 read-only 创建和恢复。
9. 新增 approval broker，保证 pending request 可恢复。
10. 新增 exec fallback。
11. 新增 Codex 页面、上下文列、composer、event stream、inspector 和 app-server start 状态条。
12. 补齐 audit、redaction、retention 和诊断 UI。
13. 用真实 `codex` CLI 做本机验收，但测试中使用 fake CLI，避免 CI 依赖 owner 账号。

## 11. 测试策略

后端单元测试：

- binary path validation。
- workspace path normalization 和 allowed root 校验。
- sandbox/approval policy 构造，确保不会生成 `--yolo`。
- env allowlist，确保 secret-like env 不泄露给子进程。
- app-server JSON-RPC response/notification 解析。
- app-server 定时 probe 的 running/stopped/failed 状态转换。
- app-server start API 的并发保护、CSRF 校验和错误脱敏。
- `codex exec --json` JSONL event 解析。
- event mapping 和 redaction。
- approval 状态机。
- 旧表存在时 migration 不失败。

后端集成测试：

- fake app-server start/thread/turn/approval/completed。
- fake app-server 未启动时 UI 状态为 stopped，点击启动后进入 starting/running。
- SSE 断线后按 sequence 补拉。
- 后端重启后 running turn 标记为 unknown。
- exec fallback 成功、失败和超时。
- workspace outside allowed roots 被拒绝。
- network/full access 请求需要显式审批或被拒绝。

前端测试：

- 空状态 composer 不像营销页，移动端不溢出。
- Thread list 搜索、置顶、归档。
- Event stream 长输出折叠和跳到底部。
- Approval panel 刷新后仍可操作。
- Diagnostics degraded 状态可理解但低噪音。
- Start app-server 按钮在启动中 disabled，失败后展示简短错误摘要。

## 12. 待确认问题

- MVP 是否必须支持 app-server thread fork，还是先只做 resume 和 archive。
- 是否需要从 Codex 本地 session 清单导入已有 CLI/TUI 会话摘要。
- 是否允许 owner 配置 `codex_cli.codex_home` 为专用目录，还是始终复用运行用户默认 Codex home。
- 是否在第一版提供图片输入，还是先只支持文本 prompt。
- 是否需要支持 Codex custom slash commands 的可视化入口，还是让用户在 prompt 中直接输入 slash command。
