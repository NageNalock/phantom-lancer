# Mail / Mox 控制面 WIP 设计

文档日期：2026-06-12  
状态：WIP / 临时开发设计文档  
生命周期：本文件用于启动 Mail 模块开发前的边界梳理。按 owner 要求，等 Mail / Mox 模块完成开发并沉淀到正式产品/技术文档后，应删除本 WIP 文档。

关联文档：

- [personal-web-terminal-product-features.md](./personal-web-terminal-product-features.md)
- [personal-web-terminal-technical-design.md](./personal-web-terminal-technical-design.md)
- [log-center-feature-design.md](./log-center-feature-design.md)
- [happy-technical-reference.md](./happy-technical-reference.md)

外部参考：

- [Mox 官网](https://www.xmox.nl/)
- [Mox config reference](https://www.xmox.nl/config/)
- [Mox command reference](https://www.xmox.nl/commands/)
- [Mox webapi package](https://pkg.go.dev/github.com/mjl-/mox/webapi)

## 1. Design Read

Reading this as: 个人服务器控制台里的自托管邮箱控制面，面向单 owner 技术用户，采用 Quiet Agent Workbench / Quiet DevOps Control Plane 语言，强调运行状态、DNS 健康、账号管理、队列可见性、投递问题定位、配置回滚和低噪音诊断。

本功能不是 Gmail/Outlook 克隆，不做团队邮箱 SaaS，不做营销邮件平台，也不把邮件协议栈直接塞进 Phantom Lancer 主进程。Phantom Lancer 应扮演控制面：管理 Mox sidecar 生命周期、配置、账号、队列、日志、事件和审计；SMTP/IMAP/Webmail/WebAPI 等邮件运行时由 Mox 独立进程承担。

## 2. 功能定位

目标是在 Phantom Lancer 中新增 `Mail` 能力域，基于 Mox 提供个人自托管邮箱系统的控制面：

- 托管一个由 Phantom Lancer 管理的 Mox 实例。
- 支持创建和管理邮箱域名、邮箱账户、地址、别名、转发和队列。
- 支持 DNS checklist，包括 MX、SPF、DKIM、DMARC、MTA-STS、TLS-RPT、PTR/forward-confirmed reverse DNS 等健康检查。
- 支持 Mox 进程启动、停止、重启、配置校验、配置回滚、崩溃恢复和可见性监控。
- 支持 Mox WebAPI 的受控封装，用于发送邮件、读取已知 message、下载 raw/part、移动、删除、flag、suppression list 和 webhook 接入。
- 支持日志、事件、投递失败、队列积压、证书状态和 DNS 风险在 UI 中可见。

核心边界：

- Phantom Lancer 是 owner 的管理入口，不是邮件传输协议实现。
- Mox 是邮件核心运行时，作为 sidecar/子进程运行。
- Phantom Lancer 不直接 import Mox SMTP/IMAP/storage internals。
- Phantom Lancer 可以 import 稳定的外部集成面，例如 Mox `webapi` client/types；也可以通过受控 CLI wrapper 调用 `mox` 命令。
- Mox config 和 data 是邮件系统的事实来源；Phantom Lancer 的 SQLite 存储模块设置、运行快照、审计、事件和必要的 UI cache。

## 3. 产品边界

### 3.1 MVP 范围

- Mail 作为独立一级导航能力域，不放入通用 `设置`。
- 支持选择或登记 Mox binary 路径，探测版本和能力。
- 支持初始化一个由 Phantom Lancer 管理的 Mox instance。
- 支持配置 Mox data dir、config dir、public hostname、internal webapi base URL、监听端口和 start-on-launch。
- 支持启动、停止、重启和状态恢复。
- 支持 `mox config test`、DNS records 建议、DNS check 和配置摘要展示。
- 支持域名 add/remove/enable/disable。
- 支持 account add/remove/enable/disable/reset password。
- 支持 address add/remove。
- 支持 alias list/add/update/remove 和 alias recipient add/remove。
- 支持 queue list、hold、unhold、schedule、fail、drop、dump 摘要。
- 支持 WebAPI send、message get、message raw get、message part get、move、delete、flags add/remove、suppression list/add/remove/present。
- 支持 incoming/outgoing webhooks，将投递事件写入 Phantom Lancer events 并通过 SSE 推送。
- 支持受控 Mox 日志查看：最近 N 行、bounded tail、secret redaction。
- 支持 Dashboard 摘要：Mox running/degraded/failed、queue depth、DNS health、last delivery failure、certificate expiry。
- 支持配置和危险操作审计。

### 3.2 非目标

- 不在 Phantom Lancer 主进程内实现 SMTP、IMAP、DKIM、DMARC、MTA-STS、DANE 或 spam filter。
- 不直接链接 Mox daemon internals，也不 fork Mox 后长期维护协议栈。
- 不做多租户、多 owner、团队共享邮箱、组织权限或部门通讯录。
- 不做营销邮件平台，不做群发 campaign，不做追踪像素，不做打开率统计。
- 不在 MVP 自研完整 Webmail。轻量收发可以基于 Mox WebAPI 和 IMAP/已知 message id 实现；完整 mailbox browsing 可以先反向代理 Mox 原生 webmail 或作为后续阶段。
- 不自动修改云厂商安全组、DNS provider、PTR 记录或公网防火墙。
- 不默认占用 25/80/443 等特权端口；需要 owner 明确配置运行身份和反向代理策略。
- 不读取或索引全部邮件正文到 Phantom Lancer SQLite。
- 不把完整邮件正文、附件、收件人列表、认证凭据或完整远端 SMTP 响应写入 service log/audit。

### 3.3 为什么采用 sidecar

Mox 是长期运行的公网协议服务，监听 SMTP/IMAP/Submission/HTTPS/WebAPI，处理外部不可信输入、慢连接、垃圾邮件、退信、队列重试和 TLS/DNS 状态。它应该有独立的：

- 进程生命周期。
- Unix 用户和文件权限。
- data/config 目录。
- 日志文件和轮转策略。
- 升级、备份、校验和回滚流程。

Phantom Lancer 只管理 sidecar，不和 Mail 运行时共享故障域。Mox 崩溃时，Phantom Lancer 仍能展示故障、日志、最近事件、重启入口和回滚入口；Phantom Lancer 升级时，也不应隐式升级邮件协议核心。

## 4. 信息架构

`Mail` 是一级导航能力域，内部二级结构建议：

- `Overview`：运行状态、DNS 健康、队列、证书、最近投递问题和风险摘要。
- `Setup`：首次初始化、binary 探测、instance 目录、public hostname、端口和运行身份。
- `Domains`：域名、DNS checklist、DKIM selector、MTA-STS/TLS-RPT、enable/disable。
- `Accounts`：账户、登录状态、地址、密码重置、禁用、容量和最近登录摘要。
- `Aliases`：别名、转发、catch-all、list-like alias 设置。
- `Delivery`：queue、hold rules、retired queue、suppression list、投递失败和 webhook 状态。
- `Messages`：轻量邮件操作入口。MVP 可以只支持基于 webhook/搜索结果/已知 message id 的详情和发送；完整邮箱浏览后置。
- `Logs`：Mox service log、bounded live tail、错误摘要、DNS check 输出摘要。
- `Events`：Mail 模块事件和审计过滤视图。
- `Settings`：Mox binary、config/data dir、webapi 凭据、端口、反向代理、备份、保留策略。

右侧 inspector 常驻低噪音信息：

- 当前 Mox PID、uptime、version、config hash。
- desired state 与 observed state。
- 最近 health probe。
- 队列积压和最近失败原因摘要。
- DNS check 最后一轮结果。
- 证书有效期。
- 最近 5 条 Mail events。

Dashboard 只展示摘要，不展开完整 Mail 配置表单。

## 5. 部署与运行模型

### 5.1 目录布局

建议默认放在 Phantom Lancer data dir 下，实际路径由运行期设置决定：

```text
<data_dir>/mail/mox/
  config/
    mox.conf
    domains.conf
    adminpasswd
  data/
    accounts/
    queue/
    tmp/
  logs/
    mox.log
  runtime/
    supervisor.json
    last-health.json
    last-dnscheck.json
```

约束：

- 新建 instance 时只写入 `<data_dir>/mail/mox/*`。
- 如果 owner 指向已有 Mox instance，默认以 `import/read-only` 模式登记，禁止覆盖现有 config/data。
- 所有路径必须经过规范化和允许根目录校验。
- config 写入必须 atomic：写 temp、`mox config test`、备份旧文件、rename。
- 失败时保留旧配置，不影响正在运行的旧进程。

### 5.2 Mox config 事实来源

Mox 使用两个配置文件：

- `mox.conf`：static config，变更后需要重启 Mox。
- `domains.conf`：dynamic config，Mox 可自动 reload；仍应在写入前执行配置测试。

Phantom Lancer 不应把完整 Mox config 复制为 SQLite truth。SQLite 存：

- Phantom Lancer 管理设置。
- 最近 config hash、version、write time。
- UI cache 和诊断快照。
- 审计和事件。

Mox config/data 仍是实际运行事实来源。页面展示时可以将 `mox config describe-*` 或本地 config parse 结果归一化成 UI 模型。

### 5.3 监听与反向代理

Mox 可能需要以下服务：

- SMTP: 25
- Submission: 587
- SMTPS: 465
- IMAP TLS: 993
- HTTPS/Webmail/WebAPI: 443 或内部端口
- HTTP ACME/MTA-STS/autoconfig: 80 或反向代理转发

MVP 建议：

- Mox public mail ports 由 Mox 直接监听，Phantom Lancer 只做预检和可见性。
- Mox WebAPI 绑定 loopback internal address，例如 `127.0.0.1:<port>`，由 Phantom Lancer 后端调用，不直接暴露给公网。
- Mox 原生 webmail 如需暴露，优先通过独立域名或受控 reverse proxy，不混进 `/api/*`。
- 如果已有 Web 服务占用 80/443，应使用 Mox 的 existing-webserver 部署思路，由 Phantom Lancer 展示需要转发的路径/域名，而不是自动改 nginx/caddy。

### 5.4 低端口与权限

绑定 25/465/587/993/80/443 可能需要 root、capability、authbind、systemd socket activation 或反向代理/端口转发。Phantom Lancer 不应默认提升权限。

UI 要明确展示：

- 当前进程用户。
- Mox 目标监听端口。
- 端口是否已占用。
- 是否需要额外系统权限。
- 失败时是 `permission denied`、`address already in use` 还是配置错误。

## 6. 需要封装的后端接口

本节描述 Phantom Lancer 对外 `/api/mail/*` 和内部 service/client 的建议边界。具体 response 字段后续实现时可按现有 API 风格细化。

### 6.1 Runtime / Supervisor API

后端内部接口：

```go
type MoxSupervisor interface {
	Status(ctx context.Context) (MoxRuntimeStatus, error)
	Start(ctx context.Context, reason StartReason) error
	Stop(ctx context.Context, reason StopReason) error
	Restart(ctx context.Context, reason RestartReason) error
	Probe(ctx context.Context, level ProbeLevel) (MoxHealthReport, error)
}
```

HTTP API：

| Method | Path | 说明 |
| --- | --- | --- |
| `GET` | `/api/mail/runtime/status` | 获取 desired/observed 状态、PID、uptime、version、config hash、health 摘要 |
| `POST` | `/api/mail/runtime/start` | 启动 Mox，CSRF，写 audit |
| `POST` | `/api/mail/runtime/stop` | 停止 Mox，CSRF，写 audit |
| `POST` | `/api/mail/runtime/restart` | 重启 Mox，CSRF，写 audit |
| `POST` | `/api/mail/runtime/probe` | 手动 health probe |
| `GET` | `/api/mail/runtime/events` | 查询 Mail runtime events |

### 6.2 Setup / Settings API

| Method | Path | 说明 |
| --- | --- | --- |
| `GET` | `/api/mail/settings` | 获取 Mail/Mox 设置，secret 只返回 masked 状态 |
| `PUT` | `/api/mail/settings` | 更新 binary、dirs、hostname、ports、startOnLaunch、webapi 设置 |
| `POST` | `/api/mail/setup/detect` | 探测 Mox binary、version、可执行权限 |
| `POST` | `/api/mail/setup/initialize` | 初始化 Phantom-managed Mox instance |
| `POST` | `/api/mail/config/validate` | 执行 config test，不写入 |
| `POST` | `/api/mail/config/apply` | 保存配置，按 static/dynamic 判断是否需要 reload/restart |
| `POST` | `/api/mail/config/rollback` | 回滚到上一份已验证配置 |
| `GET` | `/api/mail/config/summary` | 当前配置摘要、hash、是否 stale |

### 6.3 Domain API

建议优先通过 Mox CLI/config adapter 封装：

- `mox config domain add`
- `mox config domain rm`
- `mox config domain enable`
- `mox config domain disable`
- `mox config dnsrecords`
- `mox config dnscheck`

HTTP API：

| Method | Path | 说明 |
| --- | --- | --- |
| `GET` | `/api/mail/domains` | 域名列表和状态 |
| `POST` | `/api/mail/domains` | 添加域名 |
| `GET` | `/api/mail/domains/{domain}` | 域名详情、DNS 状态、DKIM 状态 |
| `DELETE` | `/api/mail/domains/{domain}` | 删除域名，危险操作确认 |
| `POST` | `/api/mail/domains/{domain}/enable` | 启用域名 |
| `POST` | `/api/mail/domains/{domain}/disable` | 禁用域名 |
| `GET` | `/api/mail/domains/{domain}/dns-records` | 获取建议 DNS records |
| `POST` | `/api/mail/domains/{domain}/dns-check` | 运行 DNS check |

### 6.4 Account / Address API

建议封装：

- `mox config account list`
- `mox config account add`
- `mox config account rm`
- `mox config account enable`
- `mox config account disable`
- `mox setaccountpassword`
- `mox config address add`
- `mox config address rm`

HTTP API：

| Method | Path | 说明 |
| --- | --- | --- |
| `GET` | `/api/mail/accounts` | 账户列表 |
| `POST` | `/api/mail/accounts` | 创建账户和首个地址 |
| `GET` | `/api/mail/accounts/{account}` | 账户详情、地址、最近登录摘要 |
| `DELETE` | `/api/mail/accounts/{account}` | 删除账户，危险操作确认 |
| `POST` | `/api/mail/accounts/{account}/enable` | 启用账户 |
| `POST` | `/api/mail/accounts/{account}/disable` | 禁用账户登录 |
| `POST` | `/api/mail/accounts/{account}/password` | 设置或生成新密码，明文只一次性返回 |
| `POST` | `/api/mail/accounts/{account}/addresses` | 添加地址 |
| `DELETE` | `/api/mail/accounts/{account}/addresses/{address}` | 删除地址 |

密码处理要求：

- 前端提交新密码时只走 HTTPS + CSRF，不写日志。
- 生成密码只一次性展示，不存明文。
- 调用 `mox setaccountpassword` 时通过 stdin 传递，不拼接到命令参数。
- audit 只记录 account、是否 generated、operator、结果，不记录密码。

### 6.5 Alias API

建议封装：

- `mox config alias list`
- `mox config alias print`
- `mox config alias add`
- `mox config alias update`
- `mox config alias rm`
- `mox config alias addaddr`
- `mox config alias rmaddr`

HTTP API：

| Method | Path | 说明 |
| --- | --- | --- |
| `GET` | `/api/mail/aliases?domain=` | alias 列表 |
| `POST` | `/api/mail/aliases` | 创建 alias |
| `GET` | `/api/mail/aliases/{alias}` | alias 详情 |
| `PUT` | `/api/mail/aliases/{alias}` | 更新 alias 设置 |
| `DELETE` | `/api/mail/aliases/{alias}` | 删除 alias |
| `POST` | `/api/mail/aliases/{alias}/recipients` | 添加 recipient |
| `DELETE` | `/api/mail/aliases/{alias}/recipients/{address}` | 删除 recipient |

### 6.6 Delivery / Queue API

建议封装：

- `mox queue list`
- `mox queue hold`
- `mox queue unhold`
- `mox queue schedule`
- `mox queue transport`
- `mox queue requiretls`
- `mox queue fail`
- `mox queue drop`
- `mox queue dump`
- `mox queue retired list`
- `mox queue retired print`
- `mox queue suppress list/add/remove/lookup`
- `mox queue webhook list/schedule/cancel/print`

HTTP API：

| Method | Path | 说明 |
| --- | --- | --- |
| `GET` | `/api/mail/queue` | 队列列表，支持 bounded filter |
| `GET` | `/api/mail/queue/{id}` | 队列消息详情摘要 |
| `POST` | `/api/mail/queue/hold` | hold matching messages |
| `POST` | `/api/mail/queue/unhold` | unhold matching messages |
| `POST` | `/api/mail/queue/schedule` | 重新调度 |
| `POST` | `/api/mail/queue/fail` | 主动失败，危险操作确认 |
| `POST` | `/api/mail/queue/drop` | 丢弃，危险操作确认 |
| `GET` | `/api/mail/queue/retired` | retired queue |
| `GET` | `/api/mail/suppressions` | suppression list |
| `POST` | `/api/mail/suppressions` | 添加 suppression |
| `DELETE` | `/api/mail/suppressions/{address}` | 删除 suppression |
| `GET` | `/api/mail/webhooks/queue` | webhook delivery queue |
| `POST` | `/api/mail/webhooks/{id}/schedule` | 重试 webhook |
| `POST` | `/api/mail/webhooks/{id}/cancel` | 取消 webhook |

队列 API 必须限制返回数量、输出大小和原始 message dump。默认只返回 envelope 摘要、queue id、时间、last error redacted 摘要。

### 6.7 Message / WebAPI API

Mox WebAPI 支持 HTTP/JSON API 和 Go client，包含 `Send`、`MessageGet`、`MessageRawGet`、`MessagePartGet`、`MessageMove`、`MessageDelete`、`MessageFlagsAdd`、`MessageFlagsRemove` 和 suppression 相关方法。

HTTP API：

| Method | Path | 说明 |
| --- | --- | --- |
| `POST` | `/api/mail/messages/send` | 发送邮件 |
| `GET` | `/api/mail/messages/{messageId}` | 获取 parsed message |
| `GET` | `/api/mail/messages/{messageId}/raw` | 下载 raw message，需要确认和大小限制 |
| `GET` | `/api/mail/messages/{messageId}/parts/{partId}` | 下载 message part / attachment |
| `POST` | `/api/mail/messages/{messageId}/move` | 移动 message |
| `POST` | `/api/mail/messages/{messageId}/flags` | 增加 flags |
| `DELETE` | `/api/mail/messages/{messageId}/flags/{flag}` | 删除 flag |
| `DELETE` | `/api/mail/messages/{messageId}` | 删除 message，危险操作确认 |

注意：

- Mox WebAPI 更适合已知 message id、webhook 自动处理和 transactional email，不等同于完整 mailbox list API。
- MVP 的 `Messages` 页面可以先基于 webhook index、queue/retired event 和手动 message id 查看。
- 完整收件箱列表如需实现，应评估两条路径：受控 IMAP client 读取 mailbox，或在 Phantom Lancer 中反向代理 Mox 原生 webmail。

### 6.8 Webhook API

| Method | Path | 说明 |
| --- | --- | --- |
| `POST` | `/api/mail/mox-webhook/incoming` | 接收 Mox incoming webhook |
| `POST` | `/api/mail/mox-webhook/outgoing` | 接收 Mox outgoing delivery webhook |
| `GET` | `/api/mail/deliveries` | 查询投递事件摘要 |
| `GET` | `/api/mail/deliveries/{id}` | 投递详情摘要 |

安全要求：

- Webhook endpoint 使用独立 secret 或 HMAC token，不依赖浏览器 session。
- webhook payload 必须大小限制。
- 默认不持久化完整正文/HTML/附件；只存 message id、from/to domain 摘要、subject redacted snippet、event、SMTP code、错误摘要、时间和关联 queue id。
- 如果后续要持久化正文索引，必须作为独立 opt-in 设置并说明磁盘占用和隐私边界。

### 6.9 Logs API

| Method | Path | 说明 |
| --- | --- | --- |
| `GET` | `/api/mail/logs/sources` | Mox 日志源列表 |
| `GET` | `/api/mail/logs/tail` | bounded tail，默认最近 200 行或 256KB |
| `GET` | `/api/mail/logs/search` | bounded search |
| `GET` | `/api/mail/logs/stream` | SSE live tail |

日志内容必须走全局 redaction。服务 `slog` 不记录完整 Mox stdout/stderr，只记录启动、退出、probe 失败和摘要。

### 6.10 Backup / Maintenance API

| Method | Path | 说明 |
| --- | --- | --- |
| `POST` | `/api/mail/backup` | 调用 Mox backup 或复制 config/data 到受控备份目录 |
| `POST` | `/api/mail/verify-data` | 调用 Mox verifydata |
| `GET` | `/api/mail/backups` | 备份列表 |
| `POST` | `/api/mail/reparse` | 维护操作，危险确认 |
| `POST` | `/api/mail/recalculate-counts` | 维护操作，危险确认 |

备份必须包含 config 和 data，并在 UI 中提醒 DKIM/TLS/账号数据的恢复边界。不得把备份文件提交进 Git。

## 7. 生命周期管理与及时发现问题

### 7.1 状态模型

Phantom Lancer 保存 desired state，并持续观测 actual state：

```text
unconfigured
configured
starting
running
degraded
stopping
stopped
failed
unknown
```

字段：

- `desired_enabled`：owner 是否希望 Mox 运行。
- `start_on_phantom_launch`：Phantom Lancer 启动时是否自动启动 Mox。
- `observed_state`：实际状态。
- `pid` / `process_group_id`。
- `started_at` / `last_seen_at` / `last_exit_at`。
- `exit_code` / `exit_signal` / `last_error_summary`。
- `config_hash` / `binary_version`。
- `health_level`：`ok`、`warning`、`critical`、`unknown`。
- `health_reasons`：结构化原因，例如 `webapi_unreachable`、`smtp_port_closed`、`dns_mx_missing`、`queue_backlog`。

### 7.2 Supervisor 要求

Mox supervisor 必须做到：

- 使用 `exec.CommandContext` + 参数数组，不拼 shell 字符串。
- 设置独立 process group，便于停止时只处理 Phantom-managed child。
- 写入 owner marker 文件，包含 binary path、config path、data dir、PID、start time、Phantom boot id。
- Phantom Lancer 重启后读取 marker，识别是否存在自己管理的旧 Mox 进程。
- 只管理 marker 匹配的 Mox，不 kill 外部手工部署的 Mox。
- Stop 先走 graceful stop，超时后再升级到 stronger termination。
- 每次 Start 前做端口预检、配置校验和目录权限检查。
- 启动成功必须通过 readiness probe，不以 `exec.Start` 成功作为 running。
- 子进程退出后记录 exit event，若 desired state 仍为 running，则按 backoff 策略重启。

### 7.3 Readiness 与 Health Probe

Probe 分层：

1. `process`：PID 存活、process group、启动时间。
2. `control`：Mox ctl 能否响应，例如 queue list/config read 类命令。
3. `webapi`：loopback WebAPI intro 或轻量方法可达。
4. `smtp`：本机 TCP connect 到 configured SMTP/submission listener，读取 banner 摘要。
5. `imap`：本机 TCP connect 到 IMAP listener，读取 capability/bannner 摘要。
6. `dns`：MX/SPF/DKIM/DMARC/MTA-STS/TLS-RPT/PTR 检查。
7. `delivery`：队列积压、最近退信、webhook 失败、suppression 激增。
8. `certificate`：证书存在、过期时间、ACME 错误摘要。

建议 cadence：

- process probe：5 秒。
- readiness probe：启动后每 1 秒，最多 30 秒。
- webapi/control probe：15 秒。
- queue/cert probe：60 秒。
- DNS deep check：5 分钟或手动触发；首次 setup 和 domain 变更后立即触发。
- log scanner：实时读取 stderr/stdout 摘要和日志 tail，但只产生限频事件。

### 7.4 Crash Loop 与 Backoff

当 Mox 异常退出：

1. 写 `mail.mox.process.exited` event。
2. 如果 `desired_enabled=false`，标记 stopped。
3. 如果 `desired_enabled=true`，进入 restart backoff。
4. 失败 1/2/3 次分别等待 2s/10s/30s。
5. 连续失败超过阈值后标记 `failed`，停止自动重启，要求 owner 介入。
6. UI 展示最近 exit code、stderr 摘要、日志入口、配置回滚入口。

不要在 tight loop 中无限重启，也不要在 service log 中刷屏。

### 7.5 配置变更与回滚

所有会影响 Mox 的配置变更必须遵循：

1. 生成新 config 到 temp file。
2. 运行 config validation。
3. 生成配置摘要 diff。
4. 写 audit pending event。
5. 备份旧 config。
6. atomic rename。
7. 如果是 dynamic config，等待 Mox reload 或执行轻量 probe。
8. 如果需要 restart，执行 restart + readiness probe。
9. readiness 失败时自动回滚旧 config，并尝试恢复旧进程。
10. 回滚失败时标记 critical，并保留所有日志入口。

UI 必须区分：

- `saved_not_applied`：已保存但需要重启。
- `running_stale`：当前运行配置不是最新配置。
- `rollback_available`：存在上一份已验证配置。
- `rollback_failed`：回滚失败，需要人工处理。

### 7.6 可见性事件

建议事件类型：

- `mail.mox.detected`
- `mail.mox.initialized`
- `mail.mox.start.requested`
- `mail.mox.started`
- `mail.mox.ready`
- `mail.mox.stop.requested`
- `mail.mox.stopped`
- `mail.mox.restart.requested`
- `mail.mox.process.exited`
- `mail.mox.health.changed`
- `mail.mox.health.probe_failed`
- `mail.mox.crash_loop_detected`
- `mail.config.validated`
- `mail.config.updated`
- `mail.config.rollback_started`
- `mail.config.rollback_completed`
- `mail.domain.created`
- `mail.domain.updated`
- `mail.domain.deleted`
- `mail.account.created`
- `mail.account.password_reset`
- `mail.account.disabled`
- `mail.alias.updated`
- `mail.queue.action`
- `mail.delivery.failed`
- `mail.delivery.deferred`
- `mail.delivery.delivered`
- `mail.webhook.failed`
- `mail.dns.warning`
- `mail.cert.expiring`

事件 payload 只记录稳定 ID、domain、account、queue id、duration、状态和错误摘要。不得记录密码、完整 token、完整邮件正文、附件、完整 subject 或完整收件人列表。

### 7.7 UI 可见性要求

Overview 首屏必须能回答：

- Mox 是否真的在跑。
- Phantom Lancer 是否希望它在跑。
- 当前暴露了哪些 mail ports。
- WebAPI 是否可达。
- 最近一次 DNS check 是什么时候，是否通过。
- 队列是否积压。
- 最近一次投递失败是什么类型。
- 证书是否接近过期。
- 最近一次配置是否已应用。
- 如果出了问题，下一步应该看哪里。

不要把核心故障藏在 Logs 页面。Logs 是证据，Overview 和 inspector 应展示诊断摘要。

## 8. 前端页面功能清单

### 8.1 Overview

- 状态条：running/degraded/failed/stopped。
- Desired vs Observed。
- PID、uptime、version、config hash。
- Mail ports：25/465/587/993/80/443 或自定义端口。
- DNS health summary。
- Queue depth、deferred count、failed count。
- Last delivery failure。
- Certificate expiry。
- 操作按钮：Start、Stop、Restart、Probe、Open logs。
- 右侧 inspector：last probes、last events、config stale。

### 8.2 Setup

- Mox binary detect。
- Instance mode：create new / import existing。
- Config dir / data dir。
- Public hostname。
- Internal WebAPI endpoint。
- Port binding preflight。
- Existing webserver mode checklist。
- Initialize action。

### 8.3 Domains

- Domain list。
- 每个 domain 的 MX/SPF/DKIM/DMARC/MTA-STS/TLS-RPT/PTR 状态。
- DNS records copy panel。
- DNS check action。
- Enable/disable/delete。
- DKIM selector/key status。

### 8.4 Accounts

- Account table：account、addresses、enabled、last login summary、mailbox size summary。
- Create account。
- Reset password。
- Disable/enable。
- Address management。
- Danger delete。

### 8.5 Aliases

- Alias list by domain。
- Recipient list。
- Add/remove recipient。
- Catch-all 标识。
- Delete alias。

### 8.6 Delivery

- Queue table：id、from domain、to domain、next attempt、last error、hold。
- Filters：account、sender domain、recipient domain、hold、next attempt。
- Actions：hold、unhold、schedule、fail、drop。
- Retired queue。
- Suppression list。
- Webhook queue。

### 8.7 Messages

MVP：

- Compose and send。
- Message detail by known ID。
- Read from webhook indexed deliveries。
- Raw download with confirmation。
- Attachment download with size/type display。
- Move/delete/flags for known message。

后续：

- Mailbox list via controlled IMAP client or Mox native webmail integration。
- Search。
- Thread view。

### 8.8 Logs

- Source selector。
- Bounded tail。
- Live tail over SSE。
- Search with max bytes/max lines。
- Redaction indicator。
- Link from health warning to filtered logs。

### 8.9 Settings

- Binary path。
- Managed instance dirs。
- Start on Phantom launch。
- WebAPI credentials and webhook secret rotation。
- Port/listener settings。
- Backup/retention。
- Log retention。
- Danger zone：detach managed Mox, delete Phantom cache, not delete Mox data by default。

## 9. 存储模型草案

SQLite 只存 Phantom Lancer 管理状态和可见性，不复制 Mox 全量邮件数据。

建议表：

```sql
CREATE TABLE IF NOT EXISTS mail_mox_settings (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  enabled INTEGER NOT NULL DEFAULT 0,
  start_on_launch INTEGER NOT NULL DEFAULT 0,
  managed_instance INTEGER NOT NULL DEFAULT 1,
  binary_path TEXT NOT NULL DEFAULT '',
  config_dir TEXT NOT NULL DEFAULT '',
  data_dir TEXT NOT NULL DEFAULT '',
  public_hostname TEXT NOT NULL DEFAULT '',
  webapi_base_url TEXT NOT NULL DEFAULT '',
  webapi_username TEXT NOT NULL DEFAULT '',
  webapi_password_ciphertext TEXT NOT NULL DEFAULT '',
  webhook_secret_ciphertext TEXT NOT NULL DEFAULT '',
  last_config_hash TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS mail_mox_runtime_state (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  desired_state TEXT NOT NULL DEFAULT 'stopped',
  observed_state TEXT NOT NULL DEFAULT 'unknown',
  pid INTEGER NOT NULL DEFAULT 0,
  process_group_id INTEGER NOT NULL DEFAULT 0,
  boot_id TEXT NOT NULL DEFAULT '',
  version TEXT NOT NULL DEFAULT '',
  started_at TEXT NOT NULL DEFAULT '',
  last_seen_at TEXT NOT NULL DEFAULT '',
  last_exit_at TEXT NOT NULL DEFAULT '',
  last_error_summary TEXT NOT NULL DEFAULT '',
  health_level TEXT NOT NULL DEFAULT 'unknown',
  health_json TEXT NOT NULL DEFAULT '{}',
  updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS mail_mox_health_checks (
  id TEXT PRIMARY KEY,
  check_type TEXT NOT NULL,
  status TEXT NOT NULL,
  target TEXT NOT NULL DEFAULT '',
  summary TEXT NOT NULL DEFAULT '',
  payload_json TEXT NOT NULL DEFAULT '{}',
  checked_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS mail_delivery_events (
  id TEXT PRIMARY KEY,
  direction TEXT NOT NULL,
  event_type TEXT NOT NULL,
  account TEXT NOT NULL DEFAULT '',
  from_domain TEXT NOT NULL DEFAULT '',
  to_domain TEXT NOT NULL DEFAULT '',
  message_id_hash TEXT NOT NULL DEFAULT '',
  queue_msg_id TEXT NOT NULL DEFAULT '',
  smtp_code TEXT NOT NULL DEFAULT '',
  error_summary TEXT NOT NULL DEFAULT '',
  payload_json TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL
);
```

Mox domains/accounts/aliases 可以不做强镜像表，优先从 Mox config 读取并缓存响应。若 UI 性能需要 cache，应加 `source_hash` 和 `refreshed_at`，并明确 Mox config 仍是事实来源。

## 10. 安全、日志与审计

### 10.1 Secret 边界

敏感信息：

- Mox admin password。
- account password。
- WebAPI password/token。
- webhook secret。
- DKIM private key。
- TLS private key。
- 邮件正文、HTML、附件。
- raw message。

处理规则：

- password 只一次性展示。
- secret 使用现有 keywrap/加密设置保存。
- CLI 调用通过 stdin 或文件权限传递 secret，禁止放入 argv。
- UI 和 API 只返回 masked 状态。
- audit 不记录明文 secret。

### 10.2 日志

Phantom Lancer service log 只记录：

- Mox 启动/停止/退出摘要。
- health probe 状态变化。
- 配置校验失败摘要。
- webhook 处理失败摘要。
- DNS check 失败摘要。

不记录：

- 完整邮件正文。
- raw message。
- 完整 MIME。
- 完整 stdout/stderr。
- 密码、token、cookie、Authorization。
- 完整 subject 或完整收件人列表。

Mox 原始日志通过 Logs 模块受控读取，带路径白名单、最大行数、最大字节数、超时和 redaction。

### 10.3 审计

必须写 audit：

- 初始化 instance。
- 启停/重启。
- 设置更新。
- domain/account/alias 变更。
- password reset。
- queue destructive 操作。
- webhook secret rotate。
- backup/restore/verify。
- 配置回滚。

审计 payload 只包含对象 ID、操作、结果、错误摘要、duration 和风险等级。

## 11. 兼容性与完整性

- Migration 只做 additive，不破坏已有 SQLite。
- 如果检测到已有 Mox config/data，默认 import/read-only，不自动覆盖。
- 所有 config update 都有旧文件备份和 rollback。
- 启动失败不删除 data。
- 删除 account/domain 前必须确认 Mox 行为和数据影响；MVP 可以先只 disable，不提供 hard delete。
- 更新 Phantom Lancer 不隐式升级 Mox binary。
- Mox binary 升级必须单独显示 release/version、执行 backup、verifydata、dry-run probe。
- Webhook 重复投递按 webhook id 幂等处理。
- 断线后前端通过 events 和 runtime status 恢复。

## 12. 分阶段实现建议

### P0: 文档与骨架

- 新增 Mail 导航占位和 API 骨架。
- 新增 Mox settings/storage schema。
- 新增 runtime status model。
- 不执行真实 Mox 操作。

### P1: Mox 探测与生命周期

- binary detect。
- managed dirs。
- start/stop/restart。
- marker 文件。
- process/readiness probe。
- Logs bounded tail。
- Overview 状态。

### P2: Config / Domain / DNS

- config validate/apply/rollback。
- domain add/remove/enable/disable。
- DNS records 和 dnscheck。
- DNS health UI。

### P3: Accounts / Aliases

- account CRUD。
- password reset。
- address management。
- alias management。
- audit and events。

### P4: Queue / Delivery Visibility

- queue list/actions。
- suppression list。
- incoming/outgoing webhooks。
- delivery events UI。

### P5: Message Light UI

- compose/send。
- message detail by known id。
- raw/part download。
- flags/move/delete。

### P6: Full Webmail Decision

二选一或并行评估：

- 受控 IMAP client 实现 mailbox list/search/thread。
- 嵌入/反向代理 Mox 原生 webmail，并只在 Phantom Lancer 中保留控制面。

## 13. 开放问题

- Mox binary 是否由 Phantom Lancer 下载、由 owner 手工安装，还是后续 release bundle 附带。
- 是否允许 Phantom Lancer 在 Linux 上配置 capability/systemd service，还是只提示 owner 手工处理低端口权限。
- 完整 Webmail 是否进入本项目边界，还是长期保持 Mox 原生 webmail。
- 是否需要 DNS provider API 自动写记录。当前建议不做，避免引入 provider secret 和公网变更风险。
- 是否需要公网可达性检查。当前建议只做本机端口和 DNS 检查，公网探测后续再做。
- 是否需要邮件正文索引。当前建议不做，保护隐私和控制 SQLite 体积。

