# Codex Desktop-like Web Client 改造方案

文档日期：2026-06-13

关联文档：

- [personal-web-terminal-product-features.md](./personal-web-terminal-product-features.md)
- [personal-web-terminal-technical-design.md](./personal-web-terminal-technical-design.md)
- [codex-cli-client-feature-design.md](./codex-cli-client-feature-design.md)

## 1. 目标收敛

本阶段 Codex 模块的核心诉求是：在浏览器里像使用 Codex Desktop 一样写代码。

这里的“像 Codex Desktop”不是复制桌面外观，也不是把 Phantom Lancer 变成 IDE，而是把主路径收敛为：

1. 选择项目。
2. 创建或继续一个 coding thread。
3. 和 Codex 对话。
4. 观察它读文件、运行命令、修改代码和请求审批。
5. 在同一个界面里 review diff、预览效果、继续追问或要求修复。
6. 最后把结果合并、保留或丢弃。

当前实现已经具备会话、工作区、审批、SSE 事件、附件、诊断、Review/Commands/Preview 抽屉等基础能力，但产品形态更像“Codex 管理控制台”。后续改造必须把常用 coding workflow 提到主界面，把低频诊断和运营能力降级到辅助入口。

## 2. 设计原则

- Codex 页面优先服务写代码，而不是服务模块管理。
- Codex 的使用体验必须尽量靠拢 Codex Desktop：新建任务、发送 prompt、流式响应、审批、中断、继续、review diff、打开 preview 和 follow-up 都应保持接近 Desktop 的顺滑程度，不能只是把后端能力粗糙铺成表单和日志列表。
- Conversation 必须支持富文本 Markdown 渲染，包括段落、列表、引用、表格、链接、代码块、inline code、任务列表和长内容折叠；assistant delta 应合并为稳定消息，避免流式输出造成跳动、重复卡片或阅读中断。
- 代码相关内容必须具备开发者可用的阅读体验：代码 fence 语法高亮、文件路径/命令/模型/ID 使用 monospace、长代码块可滚动、复制按钮、diff hunk 可扫描。
- 图片输入属于 Codex 自己的对话闭环，不能依赖或耦合多媒体资源库。Codex composer 必须支持和 Codex Desktop 接近的图片输入体验：文件选择、拖拽、从剪贴板直接粘贴截图、多图附件、附件缩略图和移除操作。上传后的图片只作为 Codex 受控 attachment 传给本机 `codex` CLI，不进入多媒体 Library，不复用 Images asset，不把 base64/data URL 写入日志、audit 或事件。
- 主屏只围绕当前 thread 展开：thread list、conversation、diff/preview/inspector。
- 审批、运行状态、变更文件、预览、错误和下一步动作必须出现在当前任务上下文里，不应要求用户跳到单独 tab 查找。
- Workspaces 是项目入口，但不是主体验。第一次配置后，用户每天看到的应该是项目和 thread，而不是运行时设置。
- Diagnostics、Capabilities、Settings、Automations 都是低频或高级能力。它们存在，但不能和“会话”同层抢主注意力。
- 安全边界不降低：仍然不暴露任意 shell、不托管 Codex token、不默认 full access、不绕过 Codex sandbox/approval。
- 保持 Quiet Agent Workbench 风格：浅色中性、低噪音、小字号、细分隔、工程化布局，不做营销欢迎页或装饰 dashboard。

## 3. 当前问题

### 3.1 信息架构偏管理台

当前 Codex 顶层为：

- 会话
- 工作区
- 收件箱
- 运行时

这对模块维护者清楚，但对“我要写代码”的用户来说，主路径被拆散了：

- 新建任务在会话。
- 审批在收件箱。
- app-server 状态在顶部 pill 和运行时。
- 诊断在运行时。
- Review、Commands、Preview 藏在 thread 内的 Tools 抽屉。

结果是用户需要理解 Phantom Lancer 自己的模块结构，而不是直接进入 Codex Desktop 式的任务流。

### 3.2 Tools 抽屉语义过泛

Review、Commands、Preview 都被放进一个 `Tools` 抽屉。它们不是同一类能力：

- Review 是代码变更验收主链路。
- Preview 是前端开发反馈主链路。
- Commands 是 owner 辅助操作和排障能力。

把它们合并为低频工具会让前端开发和代码 review 的关键动作不够显眼。

### 3.3 缺少 worktree-first 心智

像 Codex Desktop 一样写代码时，用户需要明确知道：

- 当前任务是否在原 workspace 上工作。
- 当前任务是否有隔离 worktree。
- worktree 来自哪个 base branch。
- 修改了哪些文件。
- 如何查看、继续修、合并或丢弃这些改动。

当前有 thread fork，但没有把 worktree/branch/isolation 作为新建任务和 review 的核心对象。

### 3.4 Diff、Preview、Conversation 未形成闭环

当前主区域以 conversation 为中心，diff/review/preview 作为抽屉能力存在。更自然的 coding workflow 应该是：

1. 看 Codex 回复。
2. 看它改了哪些文件。
3. 打开 diff。
4. 如是前端任务，打开 preview。
5. 在 diff 或 preview 上提出修复要求。
6. Codex 继续修改。

这些动作应在当前 thread 内连续完成。

## 4. 目标信息架构

### 4.1 Codex 一级页面

Codex 作为一级导航保持不变，与 Codex Gateway 并列。

Codex 页面内部不再优先暴露多个管理 tab，而采用三栏工作台：

```text
┌───────────────┬───────────────────────────────┬────────────────────┐
│ Project/Thread │ Current Thread                 │ Inspector/Review   │
│ list           │ Conversation + Composer         │ Diff/Preview/State │
└───────────────┴───────────────────────────────┴────────────────────┘
```

左栏：

- 当前项目选择。
- 新建 coding thread。
- 搜索 thread。
- 最近 thread。
- pinned thread。
- archived 入口弱化。

中栏：

- thread 标题。
- thread 状态。
- conversation transcript。
- command/file/approval 事件折叠在消息流中。
- 底部 composer。

右栏：

- 当前 workspace/worktree 状态。
- app-server 状态。
- active turn。
- pending approval。
- changed files。
- diff/review。
- preview。
- recent errors。

### 4.2 顶部和低频入口

页面顶部只保留当前任务需要的状态：

- app-server state。
- running/queued/approval count。
- 当前 model。
- 当前 sandbox/approval policy。

低频入口放到右上角 `More` 或 `Runtime` 抽屉：

- Workspaces 管理。
- Diagnostics。
- Capabilities。
- Settings。
- Legacy data。
- Event retention。
- Automations。
- Notifications。

审批不应只存在于 Inbox。pending approval 必须在当前 thread 的 conversation 和 inspector 中出现；Inbox 只作为跨 thread 汇总入口。

## 5. 核心任务流

### 5.1 首次进入

当 Codex 未配置或不可用：

- 主区域显示轻量 setup state。
- 显示 `codex` binary、auth、sandbox、app-server 四个状态。
- 提供 `重新探测`、`启动 app-server`、`打开 Codex 设置`。
- 不做大欢迎页，不展示营销文案。

当没有 workspace：

- 左栏显示 `添加项目`。
- 只允许从全局 allowed roots 内登记 workspace。
- 登记完成后立即进入新建 thread composer。

### 5.2 新建 coding thread

新建 thread composer 应包含：

- Project/workspace。
- Task prompt。
- Model。
- Mode：`Code` / `Ask`。
- Execution：`Current workspace` / `Worktree`。
- Sandbox：`Read-only` / `Workspace write`。
- Approval：默认 `on-request`。
- Optional attachments。

附件体验要求：

- 支持点击选择图片。
- 支持拖拽图片到 composer。
- 支持从剪贴板粘贴图片，尤其是系统截图后直接 `Cmd/Ctrl+V`。
- 支持一次选择、拖拽或粘贴多张图片，数量上限与后端 `MaxAttachmentsPerReq` 保持一致。
- 已添加附件显示缩略图、文件名、大小和移除按钮。
- 图片附件只进入 Codex attachment 临时存储，并以本地文件路径交给 `codex` CLI 的 `localImage` 输入。
- 不接入多媒体资源库，不提供“从多媒体选择图片发给 Codex”的跨模块入口，避免 Codex 写代码主链路被外部素材库复杂化。

默认策略：

- `Ask` 默认 read-only。
- `Code` 在 trusted workspace 默认 workspace-write + on-request。
- Git workspace 默认建议 `Worktree`，非 Git workspace 隐藏 worktree 选项。
- 不提供 full access 快捷入口。

### 5.3 任务执行中

执行中界面必须持续可见：

- Codex 是否正在 thinking/running/waiting approval。
- 最近正在执行的命令摘要。
- 最近修改的文件。
- 是否有 pending approval。
- 是否可以 interrupt。
- 是否可以 steer。

事件渲染规则：

- assistant delta 合并成一条消息。
- 命令和工具调用用低噪音状态行展示，默认折叠详细 stdout/stderr。
- 文件变更进入 changed files 列表，同时在 conversation 中出现摘要。
- 审批请求以内联卡片出现，支持 approve once、deny、interrupt。

### 5.4 Review 和 Diff

右栏默认显示当前 thread 的 `Changes`：

- Changed files list。
- 每个文件的 added/modified/deleted/renamed 状态。
- Diff viewer。
- Per-file open/collapse。
- Copy path。
- Add review comment。
- Ask Codex to fix selected file/comment。

Review 不再是低频 Tools 抽屉里的一个 tab，而是 coding thread 的核心 inspector view。

### 5.5 Preview

前端项目或有本地 dev server 的项目，应支持 thread 内 Preview：

- 记录 preview URL。
- 支持 open/navigate/refresh。
- 展示当前 preview 状态。
- 支持把 preview comment 附加到 thread。
- 支持从 preview 直接发送“修复这个界面问题”的 follow-up。

Preview 可以作为右栏和中栏之间的可切换 inspector view，不应藏在 Tools 抽屉第三层。

### 5.6 完成后

任务完成后提供明确下一步：

- Continue。
- Review changes。
- Run command。
- Open preview。
- Create follow-up。
- Archive thread。
- 如果是 worktree：Merge/apply back、discard worktree。

如果后端暂时不能实现 merge/apply/discard，UI 也必须显示当前状态和下一步限制，避免用户不知道改动在哪里。

## 6. 功能分层

### 6.1 P0：像 Desktop 一样写代码的最短路径

P0 目标：打开 Codex 后，用户能完成一次真实代码修改，不需要离开当前 thread。

必须完成：

- 三栏 Codex 工作台。
- 新建 coding thread composer。
- 当前 thread conversation + composer。
- app-server 状态内联展示和启动。
- pending approval 内联处理。
- changed files 摘要。
- diff/review 从 Tools 抽屉提升到右栏。
- interrupt、steer、continue 可见。
- 低频 Runtime/Settings 收进抽屉或二级入口。

可以暂缓：

- 完整 worktree lifecycle。
- 自动 merge。
- Appshots。
- 多 agent orchestration。
- 完整 browser automation。

### 6.2 P1：Worktree 和 Review 闭环

P1 目标：让每个 coding thread 有明确隔离边界和验收流程。

新增：

- 新建 thread 时选择 current workspace 或 worktree。
- worktree path summary、base branch、current branch、dirty status。
- changed files 常驻。
- diff viewer 更完整。
- review comment 和 ask-to-fix selected diff。
- discard worktree。
- apply/merge back 的受控入口。

安全要求：

- worktree 只能位于 allowed roots 或受控 runtime root。
- merge/apply 必须二次确认。
- discard 必须确认影响范围。
- 所有合并、丢弃、命令和审批写 audit。

### 6.3 P2：Preview 和本地环境

P2 目标：前端任务可以在 Web 内完成“改代码 -> 看效果 -> 评论 -> 修复”闭环。

新增：

- per workspace preview configuration。
- dev server command policy。
- preview URL allowlist。
- preview refresh。
- preview comment attach to thread。
- preview health/status。
- 最近 preview sessions。

约束：

- 不直接暴露任意内网 URL 给浏览器。
- preview proxy 必须做 path、host、timeout 和响应大小限制。
- dev server command 必须走 workspace policy 和 audit。

### 6.4 P3：自动化和跨 thread 汇总

P3 才处理更偏管理台的能力：

- Automations。
- Notifications。
- Cross-thread approval inbox。
- Background task triage。
- Runtime diagnostics dashboard。

这些能力保留，但不能压过 P0/P1 的 coding 主路径。

## 7. 当前实现调整建议

### 7.1 CodexView

现状：

- 顶层 tab 是 `会话 / 工作区 / 收件箱 / 运行时`。

调整：

- 默认只显示 Desktop-like workbench。
- Workspaces、Inbox、Runtime 进入右上角二级入口。
- Pending approval 数量保留在顶部状态，但点击后打开当前 thread approval 或跨 thread inbox。

### 7.2 ThreadsTab

现状：

- 已经接近三栏布局：ThreadList + ThreadWorkspace + ThreadInspector。

调整：

- 把它提升为 Codex 首页主体。
- 左栏加入 project/workspace 切换和更明显的新建 coding thread。
- 空状态直接进入 new thread composer。
- ThreadInspector 承担 changed files、review、preview，而不是只显示静态状态。

### 7.3 ThreadP1Panels

现状：

- `Tools` 抽屉包含 Review、Commands、Preview。

调整：

- 拆掉泛化 `Tools` 入口。
- Review/Changes 进入右栏默认 view。
- Preview 进入右栏 view 或主区域 split view。
- Commands 降级为右栏 `Run` 或 `More` 中的辅助能力。

### 7.4 WorkspacesTab

现状：

- 作为顶层 tab 管理 workspace。

调整：

- 变成 `Projects` 管理抽屉或设置页。
- 日常使用中只在左栏 project selector 出现。

### 7.5 ApprovalsTab / NotificationsTab / AutomationsTab

现状：

- 同处 `收件箱` 二级 tab。

调整：

- 当前 thread 的 approval 必须内联。
- 跨 thread approvals 保留为 inbox。
- Notifications/Automations 移到 Runtime/More。
- Automations 不作为写代码主路径入口。

## 8. 数据和 API 增量

### 8.1 Thread worktree metadata

建议在 `codex_cli_threads` 或相邻表中补充：

- `execution_mode`: `workspace` / `worktree`
- `worktree_id`
- `base_branch`
- `branch_name`
- `worktree_path`
- `worktree_status`
- `merge_status`
- `discarded_at`

兼容要求：

- 旧 thread 默认 `execution_mode = workspace`。
- 不迁移旧 thread 的实际文件。
- 旧 thread 继续可读、可继续，除非其 workspace 已不可用。

### 8.2 Changed files API

新增或稳定化：

- `GET /api/codex/threads/{id}/changes`
- `GET /api/codex/threads/{id}/diff`
- `POST /api/codex/threads/{id}/review-comments`
- `POST /api/codex/review-comments/{id}/resolve`
- `POST /api/codex/threads/{id}/follow-up-from-review`

响应必须：

- 限制 diff 大小。
- 支持 truncated 标记。
- 路径只在 workspace 边界内。
- 错误摘要脱敏。

### 8.3 Worktree lifecycle API

P1 增量：

- `POST /api/codex/worktrees`
- `POST /api/codex/worktrees/{id}/merge`
- `POST /api/codex/worktrees/{id}/discard`
- `GET /api/codex/worktrees/{id}/status`

所有写操作需要 CSRF 和 audit。

### 8.4 Preview API

P2 增量：

- `GET /api/codex/threads/{id}/preview-sessions`
- `POST /api/codex/threads/{id}/preview-sessions`
- `PATCH /api/codex/preview-sessions/{id}`
- `DELETE /api/codex/preview-sessions/{id}`
- `POST /api/codex/preview-sessions/{id}/comments`

Preview 只能代理允许的 local/loopback 或 workspace policy 指定目标。

## 9. UI 验收标准

### 9.1 首屏

- 打开 Codex 后，不需要选择“运行时”或“工作区”才能开始写代码。
- 已有项目时，首屏能看到最近 thread 和新建任务入口。
- 当前 thread 存在时，首屏直接显示 conversation、composer、inspector。

### 9.2 任务中

- 用户能一眼看出 Codex 是否在运行、等待审批、失败或完成。
- 用户能一眼看到修改了哪些文件。
- 用户能在当前 thread 内 approve/deny/interrupt。
- 用户能在当前 thread 内继续追问或 steer。

### 9.3 任务后

- 用户能 review diff。
- 用户能把某个 diff/comment 作为 follow-up 发给 Codex。
- 用户能打开 preview 并把视觉问题转成 follow-up。
- 如果有 worktree，用户能理解如何 merge/apply/discard。

### 9.4 视觉

- 不使用大 hero、大欢迎区、营销 CTA。
- 不使用高饱和渐变、玻璃拟态、AI 紫蓝装饰。
- 右栏是 inspector，不是厚重卡片堆叠。
- 状态色只表达语义：green success/running、orange warning/approval、red danger/failure。
- 按钮、输入、列表、抽屉、panel 保持小圆角、细边框、低阴影。

## 10. 非目标

- 不实现完整 IDE。
- 不直接暴露任意 shell。
- 不绕过 Codex CLI 的 sandbox、approval、AGENTS.md、MCP、skills 和 config。
- 不把 Codex 会话变成 OpenAI-compatible API；该能力属于 Codex Gateway。
- 不做团队协作、多用户权限、PR 云工作流。
- 不把多媒体 Library 作为 Codex 图片输入来源。Codex 的图片输入只通过对话框附件闭环完成。
- 不为了模拟 Desktop 而牺牲服务器 Web 控制台的安全边界。

## 11. 与 Codex Desktop 的差异检查清单

本节用于持续检查“像 Codex Desktop 一样写代码”的体验差距。它包含文档前文已定义的能力，也包含容易漏掉的细节。实现时应优先补会影响日常 coding flow 的差异；不适合个人服务器 Web 控制台边界的能力必须明确保持非目标或低优先级。

### 11.1 Composer 和输入体验

- `P0` 图片粘贴：Codex composer 支持剪贴板图片粘贴，截图后可直接粘到对话框。
- `P0` 多图片附件：点击选择、拖拽、粘贴都支持多张图片，并和后端附件上限一致。
- `P0` 附件预览：图片附件显示缩略图、文件名、大小、上传状态和移除入口。
- `P0` 快捷键：代码任务 composer 和只读 chat composer 都支持 `Cmd/Ctrl+Enter` 发送；运行中的 turn 支持同样快捷键追加输入。
- `P1` 发送前可编辑 follow-up：从 review/preview 生成的 follow-up 应填入 composer，而不是直接发送。
- `P2` 输入历史：可选支持本 thread 内最近 prompt 草稿恢复，避免刷新或误切换后丢失未发送输入。

### 11.2 Conversation 和阅读体验

- `P0` assistant 流式输出应合并为稳定消息，不出现重复消息、跳动卡片或不可读 token delta。
- `P0` Markdown/GFM、代码高亮、代码复制、长代码横向滚动必须稳定。
- `P1` 命令、文件变更、审批、工具调用作为低噪音状态行或折叠详情，不把完整日志淹没对话。
- `P1` 长任务应持续显示当前 turn 状态、最近命令、最近变更和是否可中断。
- `P2` 支持按 turn 或文件快速定位相关输出、diff、review comment。

### 11.3 Project、Thread 和 Worktree

- `P0` 选择项目、创建 thread、继续最近 thread 不需要进入运行时或设置页。
- `P0` 新建 coding thread 支持 task prompt、model、mode、execution、sandbox、approval 和附件。
- `P1` Git workspace 默认建议 Worktree，非 Git workspace 隐藏或禁用 Worktree 并解释原因。
- `P1` Worktree 状态展示 base branch、branch、worktree path summary、dirty status、apply/merge/discard 状态。
- `P1` apply/discard 必须二次确认并写 audit；apply 成功后必须说明原工作区出现未提交修改，worktree 仍可用于对照或丢弃。
- `P2` 支持导出 patch 或复制手动合并命令，作为 apply 失败或用户不想自动写回时的连续下一步。
- `P4` Cloud environment 不接入，除非后续单独设计远端环境、账号和权限边界。

### 11.4 Review、Preview 和验证闭环

- `P0` Review/Changes 是右侧 inspector 核心视图，不放入泛化 Tools 抽屉。
- `P0` 支持从 diff 行或 review comment 生成可编辑 follow-up。
- `P1` changed files list 应常驻或易于扫描，文件级展开/折叠、diff hunk 定位稳定。
- `P2` Preview 支持 workspace 级默认 URL、最近 preview session、刷新和健康状态。
- `P2` Preview comment 可填入 composer 形成可编辑 follow-up。
- `P2` Preview proxy 继续限制 local/loopback、workspace file 和显式 allowlist 公共 URL，禁止私网探测。
- `P3` Appshots / 系统截图 / 可访问性文本采集不进入近期实现；服务器环境默认没有桌面 GUI，且隐私风险高。

### 11.5 Commands、Local Environment 和自动化

- `P1` Run/Commands 是 owner 显式触发的受控命令面，不是无限制 shell。
- `P1` 项目代码命令必须经过风险评估、超时、输出裁剪、redaction 和本机 OS sandbox；沙箱不可用时拒绝执行。
- `P2` 可选支持 workspace 级 preview/dev server 启动配置，但必须走命令策略和 audit。
- `P2` Local Environment setup scripts/actions 不自动执行；如未来支持，必须 owner 显式配置、可见、可中断、可审计。
- `P3` Automations、Notifications、Triage 是低频和后台能力，不应回到主工作流抢占会话/工作区主路径。

### 11.6 Git 远端和团队能力

- `P4` Commit、push、PR 创建和远端 review context 暂不做。原因是远端认证、作者身份、签名、分支保护和组织信息边界复杂。
- `P4` 多人协作、共享会话、团队权限不做，保持单 owner 控制台定位。
- `P4` Computer Use、桌面 GUI 控制、鼠标键盘注入不做，避免越过 workspace 安全边界。

## 12. 推荐实施顺序

1. 先改 Codex 首页信息架构：让 `ThreadsTab` 成为默认 Desktop-like workbench。
2. 把 current thread approval 内联到 conversation/inspector。
3. 把 Review/Changes 从 `Tools` 抽屉提升到右栏。
4. 把 Preview 从 `Tools` 抽屉提升为右栏 view。
5. 收起 Workspaces/Runtime/Inbox 到 `More` 或高级二级入口。
6. 补 changed files/diff API 的稳定返回和截断策略。
7. 做 worktree metadata 和新建 thread execution mode。
8. 做 worktree merge/discard。
9. 做 preview 配置和 comment follow-up。
10. 补齐 Codex composer 的剪贴板图片粘贴、多图附件和附件缩略图。
11. 最后整理 Automations/Notifications，避免主路径回到管理台形态。
