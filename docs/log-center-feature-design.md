# 日志中心查看模块与日志治理设计

文档日期：2026-06-05
适用范围：Phantom Lancer 的服务运行日志、应用日志查看、Codex/Images/V2Ray 相关排障日志，以及后续新增功能的日志打印约束。

## 1. 当前实现 review

### 1.1 日志写入与轮转现状

- Go 主进程在 `cmd/phantom-lancer/main.go` 使用 `slog.NewJSONHandler(os.Stdout, LevelInfo)`，日志输出到 stdout，没有内置文件写入、轮转或清理。
- `scripts/manage.sh` 用 `nohup "$START_SCRIPT" >>"$LOG_FILE" 2>&1 &` 捕获 stdout/stderr，默认写入 `.phantom-data/logs/phantom-lancer.log`，`scripts/manage.sh logs` 只做 `tail -f`。
- 配置文件、环境变量和 `internal/config` 目前没有日志文件、最大大小、保留份数、保留天数等字段。
- 项目没有使用 lumberjack/logrotate/journald 配置，也没有定时清理服务日志的后台任务。
- Images 模块有 `historyRetention` 和 `PruneImageGenerationJobs`，只清理图片生成 job 与本地图片资产，不属于服务日志轮转。
- SQLite 中的 `events`、`audit_events` 目前只限制读取数量，没有 retention、归档或清理逻辑。

结论：当前项目没有自动日志轮转，也没有服务日志自动清理。只有 Images 历史有按条数裁剪的业务数据清理。

### 1.2 现有日志与事件边界

- 服务日志：`slog` 只覆盖启动、初始化失败、HTTP server 失败、shutdown 失败、panic recovery、少量 Images/V2Ray 内部失败。
- 审计事件：登录、工作区、Codex、Images、V2Ray 配置和控制操作已进入 `audit_events`，用于操作追踪，不应替代运行日志。
- 持久事件：Codex exec stdout/stderr、Codex session event、Images job event、V2Ray service event 进入 `events` 表，并通过 SSE 补拉或实时推送。
- 外部应用日志：产品文档已设计“日志中心”，但当前没有日志源登记、日志文件白名单、tail API 或前端日志页面。

### 1.3 关键报错日志覆盖评估

已有覆盖较好的部分：

- 配置加载、SQLite 打开、runtime settings 初始化、静态资源加载等启动失败会写 error。
- Codex exec job 的失败会写入 job 状态和 `job.failed` 事件，stderr 会作为 `process.stderr` 事件保存。
- Images provider 调用失败会进入 job error、事件和 audit。
- V2Ray 启停控制会进入 audit，运行状态保留 `lastError`。
- 登录退避和限流会写 audit，且不记录密码明文。

需要补齐但要控制数量的部分：

- HTTP 5xx 当前大多只返回 `writeError`，不写服务日志；需要仅对 5xx、panic、慢请求和安全异常写结构化日志，不记录所有正常 2xx/4xx。
- panic recovery 只记录 panic 值，缺少 method、path、request id、remote ip 摘要。
- Codex app-server stderr 目前被完全丢弃；应保留最近 N 行 ring buffer，并只在启动失败、RPC 失败或进程退出时写摘要事件/日志。
- Codex app-server stdout 的非法 JSON 当前直接忽略；应按计数聚合为 warning，避免逐行刷屏。
- `Store.AppendEvent`、`AddAudit`、SSE backlog 读取等失败多处被忽略；应在调用边界写 warn，但不递归写 audit。
- V2Ray 已配置 console log，当前没有 `LogBridge`；应只桥接 warning/error，access log 默认关闭。
- `v2ray.append` 每个成功事件都写 `Info("v2ray event")`，后续若接入高频日志桥接必须去掉成功路径逐条 info，改为错误或聚合摘要。
- Images 本地资产保存失败只体现在 `storeFailures` 事件，应补充受控 warning，包含 job id 和失败数量，不包含远程 URL query、base64 或 prompt 全文。

## 2. 设计目标

- 在 Web 上集中查看多个日志源，包括 Phantom Lancer 服务日志、工作区关联日志、V2Ray 运行日志、Codex/Images 的事件型日志。
- 每个日志默认折叠，只展示摘要；点击某个日志后才加载和展示内容，避免首屏读大文件。
- 兼容多个日志同时存在：按来源、工作区、模块、更新时间、大小、错误计数和状态组织。
- 日志内容尽量富文本化：时间、级别、组件、错误、堆栈、路径、URL、请求 id、JSON 字段、ANSI 颜色、搜索命中都可高亮。
- 保持个人单机部署、Go 后端、SQLite、SSE、受控权限边界，不引入复杂外部 observability 平台。
- 日志数量可控：只记录排障必要信息，不把高频正常路径、完整 stdout/stderr、大 prompt、图片 base64、secret 写入服务日志。

非目标：

- 不做多租户日志审计平台。
- 不开放任意路径日志浏览。
- 不把全部日志内容长期写入 SQLite。
- 不实现完整全文索引系统；MVP 只做有限范围搜索和 tail。

## 3. 信息架构与前端设计

### 3.1 Design Taste pre-flight

本模块按 `agents.md` 的 Quiet Agent Workbench / Quiet DevOps Control Plane 风格设计：

- 日志是一级全局能力，放在左侧主导航 `日志`，不塞进通用设置页。
- 页面使用工作台结构：左侧日志源列表，中间日志内容，右侧 inspector。
- 默认浅色中性底、细边框、小圆角、低对比选中态；warning/error 使用语义色，不使用营销 hero、插画、渐变或装饰动效。
- 信息密度较高但可扫描，技术值使用 monospace。

### 3.2 页面结构

一级导航新增：

- `日志`：集中日志源、tail、搜索、排障入口。

日志页布局：

- 左侧 `Log Sources`：按模块和工作区列出日志源。
- 中间 `Log Workspace`：日志摘要列表 + 点击后的内容查看器。
- 右侧 `Inspector`：当前日志源元数据、读取范围、轮转状态、错误摘要、关联操作。

移动端：

- 日志源列表收进 drawer。
- Inspector 移到内容下方。
- 内容区保留 monospace、横向滚动和换行开关，避免长行撑破页面。

### 3.3 日志源列表

每个日志源以折叠摘要行展示，默认不打开内容：

- 名称：`phantom-lancer.log`、`app.log`、`codex exec events`、`v2ray runtime`。
- 来源：`service`、`workspace`、`codex`、`images`、`v2ray`。
- 状态：available / stale / unreadable / rotated / too_large。
- 最近更新时间、文件大小、最近 error/warn 数量。
- 关联对象：工作区名、服务名、job id 或 module。

点击日志源后：

- 首次请求最近 200 行或最近 256KB，二者取更小。
- 内容区展开并显示加载态。
- 用户可再点击其他日志源并行查看；MVP 建议同屏只保持一个 active content，已打开日志保留折叠状态和 cursor。

### 3.4 富文本日志查看器

内容渲染规则：

- JSON line：解析为结构化行，突出 `time`、`level`、`msg`、`component`、`operation`、`error`、`requestId`。
- 普通文本：按正则识别 timestamp、level、路径、URL、错误码、堆栈行。
- ANSI：保留安全颜色语义，或在不可信内容中转为 class，不直接注入 HTML。
- error/warn：整行轻背景高亮，error 使用红色语义，warning 使用橙色语义。
- stack trace：缩进成可折叠 block，默认展开 error 附近上下文。
- 搜索命中：高亮关键词，并在右侧 inspector 显示命中计数。
- secret 脱敏：渲染前再次识别 token、API key、password、UUID 等敏感片段并 mask。

查看器控制：

- `Live tail` 开关，默认关闭。
- `Pause` / `Resume`，live 模式下可暂停滚动。
- 搜索框，默认只搜当前加载范围。
- 级别过滤：all / error / warning / info。
- 时间范围：latest / 15m / 1h / custom，MVP 可先只支持 latest。
- 行数上限：200 / 500 / 1000，默认 200，最大 1000。
- 换行开关、JSON 展开/收起、复制选中内容。
- `发送给 Codex 分析`：只发送当前选中的日志片段或当前过滤结果摘要，不发送整文件。

## 4. 后端接口设计

### 4.1 日志源模型

```go
type LogSource struct {
    ID          string
    Kind        string // file, event, service
    Module      string // phantom, workspace, codex, images, v2ray
    WorkspaceID string
    Name        string
    Path        string // file source only, response 中可按权限显示
    Glob        string // rotated family, optional
    Enabled     bool
    MaxReadBytes int64
    CreatedAt   string
    UpdatedAt   string
}
```

MVP 日志源：

- `phantom-service`：默认读取 `data_dir/logs/phantom-lancer.log` 或配置的 `PL_LOG_FILE`。
- `workspace-log`：工作区中显式登记的日志文件，必须通过路径白名单。
- `codex-exec-events`：从 `events` 表读取 `exec_job` 的 stderr/output 事件，作为事件型日志。
- `v2ray-runtime`：从 V2Ray warning/error log bridge 读取，MVP 可先使用 `v2ray_service` 事件。

### 4.2 API

- `GET /api/logs/sources`
  - 返回日志源列表和轻量 metadata。
- `GET /api/logs/sources/{id}/tail?cursor=&limit=200&maxBytes=262144&level=&q=`
  - 返回最近日志行，默认不读全文件。
- `GET /api/logs/sources/{id}/stream?cursor=`
  - SSE live tail，仅在用户打开日志并启用 live 后连接。
- `GET /api/logs/sources/{id}/search?q=&limit=200&maxBytes=4194304`
  - MVP 只扫描最近有限字节，超限返回 `truncated=true`。
- `POST /api/logs/sources/{id}/codex-analysis`
  - 后续能力，只允许发送用户选中片段或后端压缩摘要。

安全要求：

- 所有接口必须登录。
- 写操作或触发 Codex 分析必须 CSRF。
- 文件路径必须规范化，并落在允许根目录或日志文件白名单内。
- 不允许读取 secret 文件、SQLite DB 文件、私钥文件、任意 `/var/log`。
- 每次读取设置 timeout、最大行数、最大字节数。
- API response 中不返回 secret 明文；路径可显示，但敏感 query、token、UUID 要脱敏。

### 4.3 Tail 与轮转识别

文件 tail 策略：

- 使用 `os.Stat` 获取 size、mtime、inode/dev（可用时）。
- 从文件末尾反向读取 bounded bytes，按行切分。
- cursor 包含 source id、inode/dev、offset、size、mtime。
- 如果新 size 小于 cursor offset，或 inode 变化，返回 `rotationDetected=true`，重新从新文件末尾 tail。
- live tail 只 follow 当前活动文件；历史 rotated 文件通过 source metadata 提示，不自动扫全部旧文件。
- 压缩日志 `.gz` MVP 不 tail，只显示为 rotated artifact。

### 4.4 日志解析与脱敏

后端返回结构化 `LogLine`，前端负责渲染：

```go
type LogLine struct {
    SourceID string
    Offset   int64
    Time     string
    Level    string
    Message  string
    Fields   map[string]any
    Raw      string
    Redacted bool
}
```

解析顺序：

1. JSON line。
2. 常见文本格式：timestamp + level + message。
3. fallback raw line。

脱敏规则：

- password、token、secret、api_key、authorization、cookie、session、csrf、xai key 统一 mask。
- 图片 base64、data URL、presigned URL query 不返回完整值。
- 超长字段裁剪并标记 `truncated=true`。

## 5. 自动轮转与自动清理设计

### 5.1 服务日志轮转

建议让 Phantom Lancer 拥有自己的服务日志文件，而不是只依赖 `nohup >> file`：

- 默认路径：`data_dir/logs/phantom-lancer.jsonl`。
- stdout 仍保留，方便 systemd/journald 或容器捕获。
- 文件 writer 使用大小 + 份数 + 天数组合轮转。

默认值：

- `maxSizeMB = 32`
- `maxFiles = 5`
- `maxAgeDays = 14`
- `compress = false`（MVP 先不压缩，便于 Web 读取）

当使用 systemd/journald 或外部 logrotate 时：

- 配置中可关闭项目内文件日志。
- 日志中心只读取显式配置的文件源。
- 文档必须说明外部轮转由部署环境负责。

### 5.2 应用日志清理

日志中心读取外部应用日志时不主动删除外部日志文件，除非该日志源明确标记为 Phantom Lancer 管理。

外部日志策略：

- 只读 tail 和搜索。
- 显示“未由 Phantom Lancer 管理轮转”的状态。
- 可在 inspector 给出建议：配置应用自身 logrotate 或 systemd journal retention。

### 5.3 SQLite 事件与审计清理

需要单独增加 retention：

- `events_retention_days`：默认 30 天，Codex session/event 可按 session 归档策略保留。
- `audit_retention_days`：默认 180 天，安全相关 audit 不随普通事件一起清。
- `max_events_per_scope`：默认 5000，防止单个 job/session 无限增长。

清理任务：

- 服务启动后执行一次轻量 prune。
- 后台每天执行一次。
- 失败只写一次 warning，避免清理失败反复刷日志。

## 6. 日志打印约束

日志分三类，不混用：

- 服务运行日志：排查程序运行问题，使用 structured `slog`。
- 业务审计：记录 owner 操作、安全决策和风险摘要，写 `audit_events`。
- 任务/会话事件：记录可恢复 UI 输出，写 `events` 并通过 SSE 推送。

推荐记录的服务日志：

- 进程启动、监听地址、数据库路径、配置路径、版本。
- 启动失败、迁移失败、存储失败、静态资源缺失。
- HTTP 5xx、panic、慢请求、安全限流触发。
- 外部进程启动失败、退出失败、stderr 摘要、超时。
- provider/network 调用失败摘要。
- 后台清理、轮转、tail reader、SSE 关键失败。

不推荐记录的服务日志：

- 每个成功 HTTP 请求。
- 每个成功事件 append。
- 高频心跳、SSE heartbeat、普通轮询。
- 完整 stdout/stderr、完整 prompt、完整日志文件内容。
- API Key、session token、CSRF、cookie、Authorization header、私钥、图片 base64、presigned URL query。

日志字段建议：

- `component`：httpapi、codex、images、v2ray、storage、logs。
- `operation`：start_server、create_session、tail_log、prune_events。
- `request_id`：后续添加 middleware 后统一注入。
- `workspace_id`、`job_id`、`session_id`、`source_id`：只记录稳定 ID。
- `status`、`duration_ms`、`error`：错误和慢路径记录。

数量控制：

- 正常路径以事件/audit 为主，不额外写服务 info。
- 重复错误按 source + operation 聚合，短窗口内只写首次和计数摘要。
- stderr 只保留 ring buffer 最近 N 行，默认 N=50；失败时写摘要，不逐行写 slog。
- 搜索和 tail 的结果不写服务日志，只写操作摘要或异常。

## 7. 关键模块补齐建议

HTTP：

- 增加 request id middleware。
- 仅记录 5xx、panic、慢请求和安全异常；字段包含 method、path pattern、status、duration、request id。
- `writeError` 不直接负责日志，避免所有 4xx 刷屏；由 handler 或 middleware 判断是否记录。

Codex：

- app-server stderr 改为 bounded ring buffer。
- app-server 启动失败、RPC 失败、进程退出时写 error/warn，并把 stderr 摘要写入 session event。
- 非法 stdout JSON 采用计数聚合 warning。
- exec job scanner error 需要进入 job event 和服务 warn。
- stdout/stderr 事件需要单 job 最大事件数或最大字节数。

Images：

- provider 失败继续写 job event + audit；服务日志只写摘要。
- 资产保存失败写 warning 摘要，包含 job id、失败数量、存储类型，不包含远程 URL query 或图片内容。
- prune 失败保留 warning，但需要聚合避免每日反复刷屏。

V2Ray：

- 保持 access log 默认关闭。
- runtime warning/error 通过 LogBridge 进入事件和日志中心，按等级和频率限制。
- start/stop/restart 失败写服务 warn/error，包含端口、config hash、action，不包含 UUID。
- 去掉高频成功事件逐条 info。

Storage/Event：

- `AppendEvent` 和 `AddAudit` 失败在调用边界写 warn，包含 event type/scope，不包含 payload 全文。
- 增加 events/audit retention prune。

## 8. 实施顺序

1. 增加日志打印约束和设计文档。
2. 增加服务日志配置、轮转 writer、redactor。
3. 增加日志源 registry 和只读 tail API。
4. 增加日志中心前端一级导航、源列表、富文本查看器和 inspector。
5. 补齐 HTTP/Codex/Images/V2Ray 的关键错误日志。
6. 增加 events/audit retention prune。
7. 增加测试：路径边界、tail 读取、轮转识别、脱敏、输出上限、富文本解析。

## 9. 验收标准

- 新启动服务后，服务日志文件会按大小/份数/天数自动轮转。
- 日志中心能列出多个日志源，每个源默认折叠，点击后才加载内容。
- 大日志文件不会被一次性读入内存；API 有最大行数和最大字节数。
- error/warn、JSON 字段、堆栈、路径、URL、搜索命中可被高亮。
- Codex app-server stderr 不再完全丢失，但也不会逐行刷服务日志。
- API Key、token、cookie、私钥、图片 base64 不出现在服务日志、audit、events 或前端日志响应明文中。
- HTTP 正常请求不会造成大量日志；5xx、panic、慢请求和安全异常可定位。
- events/audit 有独立 retention，不与服务日志轮转混淆。
