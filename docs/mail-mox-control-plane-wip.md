# Mail / Mox 控制面 WIP 设计

文档日期：2026-06-12（最后更新 2026-06-12）
状态：WIP / 临时开发设计文档
生命周期：本文件用于启动 Mail 模块开发前的边界梳理。按 owner 要求，等 Mail / Mox 模块完成开发并沉淀到正式产品/技术文档后，应删除本 WIP 文档。

---

## 0. 不可变约束（HARD CONSTRAINTS）

> 以下 4 条是全文档的前提，任何设计不得绕过。

| # | 约束 | 说明 |
|---|---|---|
| C1 | **Phantom 永远不绑定 80 / 443** | Phantom HTTPS 默认端口 `10443`，可配置但不允许落入 1-1023 范围。 |
| C2 | **不对接反向代理** | Phantom 不自动管理 nginx / Caddy / Apache / 云 LB，也不生成反向代理配置。UI 上只输出纯文本提示，绝不写入系统配置。 |
| C3 | **Phantom 是系统级配置的唯一入口** | 账户 / 域名 / 别名 / 队列 / 运行时 / 证书 等系统级变更只能通过 Phantom API 发起，禁止手工修改 `mox.conf` / `domains.conf`。检测到外部修改时强制用户二选一，绝不静默覆盖。 |
| C4 | **不做 Mox binary 自动升级** | 仅提供下载 / 安装 / 卸载 / 启动 / 停止 / 重启；版本升级不提供自动化路径，UI 只做版本检测与告警，升级由 owner 手动完成。 |

---

关联文档：
- [personal-web-terminal-product-features.md](./personal-web-terminal-product-features.md)
- [personal-web-terminal-technical-design.md](./personal-web-terminal-technical-design.md)
- [log-center-feature-design.md](./log-center-feature-design.md)
- [happy-technical-reference.md](./happy-technical-reference.md)
- [closed-loop-tls-feature-design.md](./closed-loop-tls-feature-design.md)

外部参考：
- [Mox 官网](https://www.xmox.nl/)
- [Mox config reference](https://www.xmox.nl/config/)
- [Mox command reference](https://www.xmox.nl/commands/)
- [Mox webapi package](https://pkg.go.dev/github.com/mjl-/mox/webapi)
- [RFC 6186 Mailbox URL Discovery](https://datatracker.ietf.org/doc/html/rfc6186)
- [RFC 8460 TLS-RPT](https://datatracker.ietf.org/doc/html/rfc8460)
- [SQLite FTS5](https://www.sqlite.org/fts5.html)

---

## 1. Design Read

Reading this as: 个人服务器控制台里的自托管邮箱控制面，面向单 owner 技术用户，采用 Quiet Agent Workbench / Quiet DevOps Control Plane 语言，强调运行状态、DNS 健康、账号管理、队列可见性、投递问题定位、配置回滚和低噪音诊断。

本功能不是 Gmail/Outlook 克隆，不做团队邮箱 SaaS，不做营销邮件平台，也不把邮件协议栈直接塞进 Phantom Lancer 主进程。Phantom Lancer 应扮演控制面：管理 Mox sidecar 生命周期、配置、账号、队列、日志、事件和审计；SMTP/IMAP/Webmail/WebAPI 等邮件运行时由 Mox 独立进程承担。

---

## 2. 功能定位与模块划分

目标是在 Phantom Lancer 中新增 `Mail` 能力域，基于 Mox 提供个人自托管邮箱系统的控制面：

- 托管一个由 Phantom Lancer 管理的 Mox 实例。
- 支持下载/安装/卸载 Mox binary（不支持自动升级，仅版本告警）。
- 支持创建和管理邮箱域名、邮箱账户、地址、别名、转发和队列。
- 支持 DNS checklist：MX、SPF、DKIM、DMARC、TLS-RPT、PTR、TLSA、Autoconfig SRV。
  > **MTA-STS 不支持**：MTA-STS 标准 (RFC 8461) 强制 policy 文件必须在 443 端口提供。受约束 C1 限制，本项目永远不提供 MTA-STS 托管。DNS checklist 中该项标记为灰色「不支持（443 端口约束）」，并附 tooltip 说明。替代方案：启用 DANE/TLSA（§5.4.4）。
- 支持 Mox 进程启动、停止、重启、配置校验、配置回滚、崩溃恢复和可见性监控。
- 支持 Mox WebAPI 的受控封装：发送邮件、读取 message、下载 raw/part、移动、删除、flag、suppression list、webhook 接入。
- **完整邮件浏览**：所有邮箱账户的文件夹列表 / 邮件列表 / 单封详情 / 附件流式下载。
- **全文搜索**：全局跨账户搜索和单账户全文搜索（标题 + 发件人 + 收件人 + 正文纯文本），支持多维度过滤。
- 支持日志、事件、投递失败、队列积压、证书状态、DNS 风险、出站速率、DNSBL 声誉在 UI 中可见。

### 2.1 核心后端模块

在 `internal/mail/` 下独立模块划分：

| 模块 | 职责 | 关键依赖 |
|---|---|---|
| `moxcli` | **PathA 封装**：所有 `mox config *` / `mox queue *` / `mox setaccountpassword` 等 CLI 调用。参数数组执行 + stdout/stderr 结构化解析。对外暴露纯函数。 | `os/exec`、Mox 指定版本 CLI |
| `moxbinary` | Mox binary 生命周期：探测版本 / 下载官方 release / 内置 checksum 校验 / 安装到受控目录 / 卸载。**不做自动升级**。 | `net/http`、代码内置的 checksum 常量表 |
| `moxsupervisor` | **独立 supervisor 模块**（不复用 `internal/supervisor`）。Mox 进程 start/stop/restart、marker 文件、process group、PID wrap 防护、orphan adopt、backoff、crash loop、graceful termination。 | `os/exec`、`syscall`、`internal/storage`、`internal/events` |
| `certmanager` | Mox 证书生命周期：**ACME DNS-01 签发**（不依赖 80/443）/ 原子写入 / 过期告警 / 续签触发 / Mox 重载。复用 closed-loop-tls 的加密存储和续期决策模型，替换签发后端为 DNS-01。 | `golang.org/x/crypto/acme` 或 `lego`、DNS provider API（Cloudflare / DNSPod / Route53，可扩展） |
| `configapply` | **10 步 apply 流水线**（§7.5）：config test、atomic rename、diff 生成、自动回滚、readiness 校验。 UI 逐行展示进度，**绝不黑屏**。 | `moxcli`、`moxsupervisor`、`internal/storage` |
| `probes` | 9 层健康探测 + reputation（DNSBL）+ cadence 调度 + 限频 + 并发控制。 | `moxsupervisor`、`net`、`internal/events` |
| `imapsync` | **IMAP 增量同步 agent**：每个活跃账户一个长连 goroutine，UID 对齐 + IDLE 推送 + 正文纯文本提取 + FTS5 索引写入。**只存纯文本 + 邮件头，不存 MIME/附件本体。** | `github.com/emersion/go-imap` v2、SQLite FTS5 |
| `search` | 全文搜索 & 邮件列表查询：跨账户 / 单账户 / 过滤 / 分页 / snippet 高亮。 | FTS5、`mail_messages` 索引 |
| `webhooks` | 入站 webhook 处理：**HMAC-SHA256 签名校验 + 15 分钟 replay 防护 + loopback-only 源地址 + 1MB 大小限制** + delivery event 写入。 | `internal/events`、`internal/storage` |

核心边界：
- Phantom Lancer 是 owner 的管理入口，不是邮件传输协议实现。
- Mox 是邮件核心运行时，作为 sidecar/子进程运行。
- Phantom Lancer 不直接 import Mox SMTP/IMAP/storage internals。
- Phantom Lancer 可以 import 稳定的外部集成面（Mox webapi client/types、go-imap）；也可以通过受控 CLI wrapper 调用 `mox` 命令。
- **系统级配置（账户/域名/别名/队列策略/运行参数）：Phantom SQLite 为唯一来源**。
- **邮件级数据（邮件本体/文件夹/flags/IMAP UID）：Mox data 为唯一来源**。
- 邮件本体（MIME / 附件）永远存在 Mox `data/` 目录；Phantom 只存邮件头 + 正文纯文本索引 + 附件元数据。

---

## 3. 产品边界

### 3.1 MVP 范围

- Mail 作为独立一级导航能力域。
- **Mox binary 管理**：版本探测、下载官方 release、内置 checksum 校验、安装到受控目录、卸载、版本低于阈值告警。
- 支持初始化一个由 Phantom Lancer 管理的 Mox instance。
- 支持配置 Mox data dir、config dir、public hostname、internal webapi endpoint（unix socket 优先）、监听端口和 start-on-launch。
- 支持启动、停止、重启和状态恢复。
- 支持 `mox config test`、DNS records 建议、DNS check 和配置摘要展示。
- 支持域名 add/remove/enable/disable。
- 支持 account add/remove/enable/disable/reset password。
- 支持 address add/remove。
- 支持 alias list/add/update/remove 和 alias recipient add/remove。
- 支持 queue list、hold、unhold、schedule、fail、drop 摘要（**不直接暴露 `mox queue dump`**）。
- 支持 Mox WebAPI 的受控封装。
- 支持 incoming/outgoing webhooks（HMAC-SHA256 + replay 防护 + loopback-only）。
- 支持受控 Mox 日志查看（bounded tail + Mail 专属 redaction + SSE live tail 采样）。
- **完整邮件浏览**（3 栏布局）+ **Compose + 草稿保存**。
- **全文搜索**（全局/单账户 + 过滤 + snippet 高亮）。
- **证书管理**：ACME DNS-01（Cloudflare/DNSPod/Route53/手动）、到期告警、原子替换、Mox 重载。
- **DNSBL 声誉监控**：8 个主流 DNSBL 的查询与告警。
- **出站速率指标**：1m/1h/24h 三窗口 + 阈值告警。
- 支持 Dashboard 摘要 + 全流程配置 audit + 10 步 apply 可见性。

### 3.2 非目标（更新版）

- 不在 Phantom Lancer 主进程内实现 SMTP、IMAP、DKIM、DMARC、DANE 或 spam filter。
- 不直接链接 Mox daemon internals，也不 fork Mox 后长期维护协议栈。
- 不做多租户、多 owner、团队共享邮箱、组织权限或部门通讯录。
- 不做营销邮件平台，不做群发 campaign，不做追踪像素，不做打开率统计。
- 不提供 MTA-STS 托管（受 C1 约束；启用 DANE/TLSA 作为替代）。
- 不自动修改云厂商安全组、DNS provider A/MX/AAAA/SPF/DKIM/DMARC/PTR **（DNS-01 挑战用的 `_acme-challenge` TXT 和 TLSA 记录除外）** 或公网防火墙。
- 不占用 80/443 端口（约束 C1）；Autoconfig 仅通过 DNS SRV（RFC 6186）方式提供。
- 不管理任何反向代理（约束 C2）。
- 不持久化完整邮件 MIME、附件内容、内联图片到 Phantom SQLite（仅存正文纯文本 + 邮件头 + 附件元数据）。
- 不把完整邮件正文、附件、收件人列表、认证凭据或完整远端 SMTP 响应写入 service log/audit。
- 不做 Mox binary 自动升级（约束 C4）。
- 不提供硬限流（仅可见性 + 阈值告警，避免误伤用户正常使用）。

### 3.3 为什么采用 sidecar（不变）

Mox 是长期运行的公网协议服务，监听 SMTP/IMAP/Submission/HTTPS/WebAPI，处理外部不可信输入、慢连接、垃圾邮件、退信、队列重试和 TLS/DNS 状态。它应该有独立的：
- 进程生命周期 / Unix 用户和文件权限 / data/config 目录 / 日志文件和轮转策略 / 安装备份校验流程。

Phantom Lancer 只管理 sidecar，不和 Mail 运行时共享故障域。Mox 崩溃时 Phantom 仍能展示故障、日志、最近事件、重启入口和回滚入口；Phantom 升级时，也不应隐式升级邮件协议核心。

---

## 4. 信息架构

`Mail` 是一级导航能力域，内部二级结构：

- `Overview`：运行状态、DNS 健康、队列、证书、最近投递问题、出站速率、DNSBL 声誉和风险摘要。
- `Setup`：Mox binary 安装/版本、instance 初始化、DNS provider token、端口 preflight、运行身份。
- `Domains`：域名、DNS checklist（8 项 + MTA-STS 灰标 + Autoconfig SRV）、DKIM selector、TLSA、enable/disable。
- `Accounts`：账户、地址、密码重置、禁用、容量、最近登录、IMAP 同步进度。
- `Aliases`：别名、转发、catch-all、list-like alias 设置。
- `Delivery`：queue、hold rules、retired queue、suppression list、投递失败、webhook 状态、出站速率。
- `Mailbox`：完整邮件浏览（账户选择 + 文件夹树 + 邮件列表 + 单封预览 + 顶部搜索框 + Compose）。
- `Logs`：Mox service log、bounded live tail over SSE、错误摘要、DNS check 输出摘要。
- `Events`：Mail 模块事件和审计过滤视图。
- `Certificates`：ACME DNS-01 状态、证书详情、到期倒计时、手动续签入口、TLSA 面板。
- `Settings`：Mox binary、config/data dir、webapi 凭据、端口、DNS provider token、备份、保留策略、全文索引开关、出站阈值、危险区。

右侧 inspector 常驻低噪音信息：
- 当前 Mox PID、uptime、version、config hash、binary 路径。
- desired state 与 observed state（含 drift 状态标志）。
- 最近 health probe 分层结果（9 层，每层独立 status dot）。
- 队列积压和最近失败原因摘要。
- DNS check 最后一轮结果。
- 证书有效期（每个证书一行，< 30 天黄色 / < 7 天红色）。
- 出站速率（1m / 1h / 24h 计数 + 阈值线）。
- DNSBL 命中情况（命中数 + 列表链接）。
- 全文索引状态（最近同步时间 / 未同步账户数 / 索引大小）。
- 最近 5 条 Mail events。

Dashboard 只展示摘要，不展开完整 Mail 配置表单。

---

## 5. 部署与运行模型

### 5.1 目录布局

默认放在 Phantom Lancer data dir 下，实际路径由运行期设置决定：

```text
<data_dir>/mail/
  mox/
    bin/
      mox                    # moxbinary 管理的受控副本（install 后存在）
      checksums.txt          # 对应版本的官方 checksum（Phantom 代码内置的副本）
    config/
      mox.conf
      domains.conf
      adminpasswd
      .marker.json           # moxsupervisor 写入（见 §7.2 schema）
    data/
      accounts/
      queue/
      tmp/
    certs/
      <hostname>/cert.pem    # certmanager 原子写入
      <hostname>/privkey.pem # 权限 0600
      <hostname>/chain.pem
      <hostname>/.link       # 原子替换用的 temp marker
    logs/
      mox.log                # 由 Mox 自己写，日志轮转也交给 Mox
    backups/
      config-<timestamp>.tar.gz
      data-<timestamp>.tar   # opt-in
    runtime/
      webapi.sock            # Mox WebAPI unix socket（推荐），权限 0600
      last-health.json
      last-dnscheck.json
      last-dnsbl.json
  index/
    mail-fts.db              # FTS5 + mail_messages 独立 SQLite（可选，便于整库删除）
```

约束：
- 新建 instance 时只写入 `<data_dir>/mail/*`。
- 如果 owner 指向已有 Mox instance，默认以 `import/read-only` 模式登记（仅感知存活 + 亚健康 + 日志），禁止覆盖现有 config/data。
- 所有路径必须经过规范化和允许根目录校验。
- config 写入必须 atomic：写 temp → `mox config test` → 备份旧文件 → rename。
- 失败时保留旧配置，不影响正在运行的旧进程。
- `certs/` 目录 0700；`privkey.pem` 0600；`runtime/webapi.sock` 0600。

### 5.2 配置事实来源（双写模型）

**系统级配置（账户 / 域名 / 别名 / 队列策略 / 运行参数）：Phantom SQLite 为唯一来源。**
**邮件级数据（邮件本体 / 文件夹 / flags / IMAP UID）：Mox data 为唯一来源。**

Mox 使用两个配置文件：
- `mox.conf`：static config，变更后需要重启 Mox。
- `domains.conf`：dynamic config，Mox 可自动 reload；仍应在写入前执行 `mox config test`。

Phantom SQLite 存：
- 系统级配置的完整期望状态（`mail_domains` / `mail_accounts` / `mail_addresses` / `mail_aliases` / `mail_alias_recipients` 表，见 §9）。
- 应用到 Mox 的最后一次 config hash（`last_applied_hash`）和时间。
- 运行状态、UI cache、诊断快照、审计、事件。
- IMAP 索引（`mail_messages` / `mail_message_bodies`）和 FTS5 全文搜索数据。

**配置同步单向性（Phantom → Mox）**：
```
UI 操作 → API → SQLite 更新 → configapply 10 步流水线 → 写 mox.conf.tmp/domains.conf.tmp
                                                              ↓
                                                         config test
                                                              ↓
                                                backup 旧文件 → atomic rename
                                                              ↓
                                    (dynamic) wait reload / (static) graceful restart
                                                              ↓
                                                       readiness probe 成功
                                                              ↓
                                           SQLite 更新 last_applied_hash
```

**配置漂移处理（约束 C3 落地）**：
- 启动时和每 10 分钟：计算磁盘上 `mox.conf` / `domains.conf` 的哈希，与 SQLite 的 `last_applied_hash` 对比。
- 如果不匹配 → 立即标记 `config_drifted = critical`：
  1. **阻塞所有系统级写 API**（账户/域名/别名/配置变更全部返回 409 `config_drifted`）。
  2. Overview 首页顶部红色 banner，强制 owner 二选一：
     - **[从磁盘导入并覆盖 SQLite]**：解析当前磁盘 config → 写回 SQLite（承认外部修改）。
     - **[用 SQLite 配置覆盖磁盘]**：重新跑 apply 流水线。
  3. 绝不自动选择任何一侧，绝不静默覆盖。

### 5.3 监听与端口分配（受 C1/C2 约束）

| 服务 | 端口 | 监听地址 | 说明 |
|---|---|---|---|
| SMTP (入站) | **25** | `0.0.0.0` / `::` | 对端 MTA 只连 25，不可更改。需 capability / authbind / iptables DNAT（UI 文本指引） |
| SMTPS | **465** | `0.0.0.0` / `::` | 隐式 TLS，不可改（主流客户端默认） |
| Submission (STARTTLS) | **587** | `0.0.0.0` / `::` | 标准端口，不可改 |
| IMAPS | **993** | `0.0.0.0` / `::` | 隐式 TLS，不可改 |
| Mox Webmail | **10444** | 可配置，默认 `127.0.0.1` | 高端口。默认只绑 127.0.0.1；用户要公网访问时可手动改为 `0.0.0.0`（UI 弹出安全风险二次确认） |
| Mox WebAPI | **unix socket** 优先 | `<data_dir>/mail/mox/runtime/webapi.sock` | **推荐**：权限 0600，只有 Mox 运行用户和 Phantom 用户（同组）能访问。如果 Mox 不支持 uds 则降级到 loopback TCP。 |
| Mox WebAPI (fallback) | **10445** | `127.0.0.1` | **不可配置为 0.0.0.0**，Settings 改会弹警告 + 二次确认 |
| Phantom HTTPS | **10443** | `0.0.0.0` | 受约束 C1，永不允许 80/443 |
| Mox HTTP (ACME) | 不用 | — | 走 DNS-01，不需要 HTTP 挑战端口 |

**端口 preflight 检查（每次 Start 前必须全部通过）**：
- 25/465/587/993/10444/10445 是否被占用。
- Phantom 10443 是否和 Mox 端口冲突。
- 分类返回错误：`permission_denied` / `address_in_use` / `config_error` / `unknown`。
- 对 `permission_denied` 输出 3 条可选解决方案的**纯文本指引**（不自动执行）：
  1. capability：`sudo setcap 'cap_net_bind_service=+ep' /path/to/mox`
  2. authbind：`authbind --deep /path/to/mox`（告知 owner 自行配置）
  3. iptables DNAT：`iptables -t nat -A PREROUTING -p tcp --dport 25 -j REDIRECT --to-port 8025`（并告知改 Mox 的 SMTP 监听为 8025）

**Autoconfig（邮件客户端自动发现）处理**：
- 受 C1 限制，无法提供 `autoconfig.example.com` 的 HTTP 服务。
- **替代方案**：UI 引导 owner 添加 DNS SRV + TXT 记录（RFC 6186），主流 MUA 支持。
  ```
  _submission._tcp.example.com.  SRV  0 1 587 mail.example.com.
  _imaps._tcp.example.com.       SRV  0 1 993 mail.example.com.
  ```
- Domains 页 DNS checklist 展示这些记录的检查状态。

### 5.4 TLS / 证书 / ACME 职责划分

#### 5.4.1 总原则

受 C1（无 80/443）和 C2（无反向代理）约束：
- **Mox 的所有 TLS 证书全部通过 ACME DNS-01 挑战签发。** 不需要 80/443 任何端口。
- **Phantom 的 HTTPS (10443) 证书独立管理**，复用 closed-loop-tls。Mail 模块不碰 Phantom 自己的证书。
- `certmanager` 模块是 Mox 证书的唯一管理者。

#### 5.4.2 DNS provider 配置

Settings 页要求 owner 配置：
- DNS provider（下拉：Cloudflare / DNSPod / Route53 / 手动模式）
- 对应 API token（通过 `internal/keywrap` 加密保存）
- 连接测试按钮：调 provider API 读一条记录验证权限。

手动模式（不给 token 的用户）：
- Phantom 生成 ACME DNS-01 挑战的 TXT 记录名和值。
- UI 弹窗引导用户**手动**添加到 DNS provider。
- 每 10 秒轮询 DNS，超时 60 分钟。
- 到期前 30 天触发 SSE + 邮件提醒用户提前操作。

#### 5.4.3 证书管理流水线（由 certmanager 实现）

```
触发（到期前 30 天 / 手动续签 / 新域名添加 / 证书缺失）
  ↓
1. 生成新的 ACME order（golang.org/x/crypto/acme 或 lego）
2. 获得 DNS-01 挑战：`_acme-challenge.<domain>` 的 TXT 值
3a. 有 DNS provider token → 调 provider API 写 TXT
3b. 手动模式 → 暂停流水线，UI 弹窗显示 TXT 名+值，用户点「我已添加」后继续
4. 等待 DNS 生效（每 10 秒查一次，最多 10 分钟；手动模式可手动确认）
5. ACME CA 验证通过 → 收到 cert + chain，匹配生成的 privkey
6. 原子写入 `<data_dir>/mail/mox/certs/<hostname>/`：
   - 写 `*.tmp` → chmod 0600 → rename 覆盖正式文件
   - 写入顺序：privkey → chain → cert（防止半新半旧组合）
7. 调用 `mox config test` 校验证书可读 + 格式合法 + 私钥匹配
8. Mox reload：
   - 如果 Mox 支持 SIGHUP 重载 TLS → 发 SIGHUP
   - 否则走 graceful restart（§7.2 停止策略）
9. 9 层 probe 里的 imap/smtp 做 TLS 握手验证成功
10. 写 `mail.cert.renewed` 事件 + 更新 UI
11. 失败 → 保留旧证书（第 6 步原子操作不会产生坏文件）
    → 标记 `mail.cert.expiring` critical
    → SSE 推送 + 日志写入完整错误
```

三重保障：原子 rename + 严格写入顺序 + reload 前 config test。

#### 5.4.4 DANE / TLSA（MTA-STS 的替代）

MTA-STS 不支持（受 C1）。DANE / TLSA 基于 DNS，不依赖 443，可以做：
- 新证书签发后，certmanager 自动计算 TLSA 记录值（`3 1 1` + SHA-256）。
- 如果有 DNS provider token → 自动更新 `_25._tcp.mail.example.com TLSA` 记录。
- 手动模式 → UI 显示记录值 + 引导手动添加。
- DNS checklist 中增加 TLSA 检查项。
- MVP 先支持「建议值生成 + 检查」，自动更新放 P2。

### 5.5 低端口与权限（增强版）

UI 要明确展示：
- 当前进程用户（whoami）。
- Mox 目标运行用户（可配置，推荐独立 `_mox` 用户，默认当前用户）。
- 每个目标端口的 preflight 结果（独立 status dot）。
- 失败分类（permission_denied / address_in_use / config_error）。
- permission_denied 下的 3 条文本指引（见 §5.3）。
- 推荐优先使用 capability（最少改动，最隔离）。

---

## 6. 需要封装的后端接口

本节描述 Phantom Lancer 对外 `/api/mail/*` 和内部 service/client。所有写 API 强制 CSRF + Audit。所有返回 HTTP 状态码遵循项目现有风格。

### 6.1 Binary / Setup API（新增）

| Method | Path | 说明 |
| --- | --- | --- |
| `POST` | `/api/mail/binary/detect` | 探测系统中已安装的 Mox（PATH + 受控目录）→ 返回 `{version, path, is_managed, meets_min_version}` |
| `POST` | `/api/mail/binary/download` | 参数 `{version: "v0.x.y"}`，从白名单 URL 下载对应版本官方 release → 校验内置 checksum → 安装到 `<data_dir>/mail/mox/bin/mox`。禁止任意 URL。 |
| `POST` | `/api/mail/binary/uninstall` | 删除受控 binary（要求 Mox 未运行 + 二次确认），保留 config/data |
| `POST` | `/api/mail/setup/initialize` | 初始化 Phantom-managed Mox instance（建目录结构 + 默认 `mox.conf` + 生成 `adminpasswd` + 写 marker） |
| `POST` | `/api/mail/setup/import` | 参数 `{config_dir, data_dir}`，登记外部 Mox 实例（read-only 模式，只开启 Status/Probe/Logs/Events） |
| `POST` | `/api/mail/setup/preflight-ports` | 端口 preflight 检查，不启动 Mox → 返回结构化结果，按端口分类 |

下载安全：
- 允许的 release URL 前缀写死在 Go 代码里（如 `https://github.com/mjl-/mox/releases/download/`）。
- 每个支持版本的 SHA-256 checksum 同样写死（随 Phantom 代码发版时更新）。
- **不做「拉最新 release」的联网查询**——避免意外版本漂移。用户要新版必须等 Phantom 发版。

### 6.2 Runtime / Supervisor API（moxsupervisor 负责）

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
| `GET` | `/api/mail/runtime/status` | desired/observed 状态、PID、uptime、version、config hash、health 摘要、`config_drifted` 标志 |
| `POST` | `/api/mail/runtime/start` | 启动 Mox（先跑 §5.3 preflight + `mox config test`，任一失败直接拒绝） |
| `POST` | `/api/mail/runtime/stop` | 停止 Mox（graceful → timeout → sigterm → sigkill 升级策略） |
| `POST` | `/api/mail/runtime/restart` | 重启 Mox |
| `POST` | `/api/mail/runtime/probe` | 手动 health probe（限频：同类 probe 未完成则取消前一个；10s 内完成过则返回缓存） |
| `GET` | `/api/mail/runtime/events` | 查询 Mail runtime events（分页） |
| `POST` | `/api/mail/runtime/resolve-drift` | 解决配置漂移，body `{action: "import_from_disk" \| "reapply_from_sqlite"}` |

### 6.3 Settings / DNS Provider / 证书 API

| Method | Path | 说明 |
| --- | --- | --- |
| `GET` | `/api/mail/settings` | 获取 Mail/Mox 设置，secret 全部返回 masked（`***`） |
| `PUT` | `/api/mail/settings` | 更新 binary、dirs、hostname、ports、startOnLaunch、webapi、DNS provider、全文索引开关、出站阈值等 |
| `POST` | `/api/mail/settings/dns-provider-test` | 仅测试 DNS provider token 连通性 |
| `POST` | `/api/mail/config/validate` | 执行 config test（基于当前 SQLite 期望状态渲染出的 temp 文件），不写入磁盘 |
| `POST` | `/api/mail/config/apply` | 触发 10 步 apply 流水线（§7.5）→ 返回 SSE stream 展示每步状态 + 进度百分比 + 最终成功/失败 |
| `POST` | `/api/mail/config/rollback` | 回滚到上一份已备份配置 |
| `GET` | `/api/mail/config/summary` | 当前配置摘要、期望/实际 diff、last_applied_hash vs 磁盘实际 hash |
| `GET` | `/api/mail/certificates` | 所有托管证书列表：hostname、not_before、not_after、days_left、issuer、状态（ok/expiring_soon/expired/error）、签发方式、上次续签时间、下次计划续签 |
| `POST` | `/api/mail/certificates/renew` | 参数 `{hostname}`，手动触发续签 |
| `POST` | `/api/mail/certificates/dns01-confirm` | 手动模式下，用户点「我已添加 TXT 记录」后继续流水线 |

### 6.4 Domain API

底层通过 `moxcli` 封装（PathA），上游是 SQLite 期望状态。UI 操作先写 SQLite，再由 configapply 异步同步到 Mox config（返回 `synced` 字段）。

| Method | Path | 说明 |
| --- | --- | --- |
| `GET` | `/api/mail/domains` | 域名列表和状态（列 `synced` = 期望是否已同步到 Mox） |
| `POST` | `/api/mail/domains` | 添加域名 → 写 SQLite → 异步触发 apply |
| `GET` | `/api/mail/domains/{domain}` | 域名详情、DNS 状态、DKIM selector/公钥、TLSA 建议值 |
| `DELETE` | `/api/mail/domains/{domain}` | 删除域名（危险操作确认 + 先检查是否仍有账户引用） |
| `POST` | `/api/mail/domains/{domain}/enable` | 启用域名 |
| `POST` | `/api/mail/domains/{domain}/disable` | 禁用域名 |
| `GET` | `/api/mail/domains/{domain}/dns-records` | 建议 DNS records：MX/SPF/DKIM/DMARC/TLS-RPT/PTR/TLSA/Autoconfig-SRV + MTA-STS 标记不支持 |
| `POST` | `/api/mail/domains/{domain}/dns-check` | 运行 DNS check（**SSR 防护：domain 必须 ∈ 已登记域名列表**） |

### 6.5 Account / Address API

底层通过 `moxcli` 封装。

| Method | Path | 说明 |
| --- | --- | --- |
| `GET` | `/api/mail/accounts` | 账户列表 + IMAP 同步进度 + 最近登录 + 容量 + synced |
| `POST` | `/api/mail/accounts` | 创建账户 + 首个地址 → 生成初始密码（一次性展示）→ apply → 自动启动 imapsync goroutine |
| `GET` | `/api/mail/accounts/{account}` | 账户详情、地址、最近登录、同步状态 |
| `DELETE` | `/api/mail/accounts/{account}` | 危险操作确认。MVP 默认只 disable（保留邮件数据），hard delete 单独走 Settings 危险区 |
| `POST` | `/api/mail/accounts/{account}/enable` | 启用账户 + 恢复 imapsync |
| `POST` | `/api/mail/accounts/{account}/disable` | 禁用账户登录 + 停止 imapsync |
| `POST` | `/api/mail/accounts/{account}/password` | 设置或生成新密码，明文一次性返回；通过 **stdin** 传给 `mox setaccountpassword` |
| `POST` | `/api/mail/accounts/{account}/addresses` | 添加地址 |
| `DELETE` | `/api/mail/accounts/{account}/addresses/{address}` | 删除地址 |
| `POST` | `/api/mail/accounts/{account}/resync` | 手动触发 imapsync 全量重同步（用户觉得搜索结果不准时使用） |

密码处理要求：
- HTTPS + CSRF，请求体不进 slog。
- 自动生成密码使用 `crypto/rand`，24 字符或等效 diceware 6 词，**只一次性展示**。
- 调用 `mox setaccountpassword` 通过 stdin，禁止 argv（防止出现在 `ps`）。
- 强度要求：≥ 12 位或 diceware 5 词以上；明显字典词前台提示。
- audit 只记录 account、是否 generated、operator、结果、耗时，不记密码。

### 6.6 Alias API

底层通过 `moxcli` 封装。HTTP API 保持原设计不变，补充：
- Alias 的 catch-all（`alias_local` 为空字符串）单独在 UI 上有视觉标识。
- list-like alias 的模式切换（谁可以向这个 alias 发信）有明显提示。

### 6.7 Delivery / Queue API

保持原设计，补充两条硬性安全约束：
- 所有 `last_error` 字段经过 Mail redaction（§10.2），去掉隐私邮箱地址、具体会话 token。
- **永远不直接暴露 `mox queue dump`**。Phantom 只实现摘要查询，完整 dump 必须 SSH 手动操作，UI 明确说明。

### 6.8 Message / WebAPI API

| Method | Path | 说明 |
| --- | --- | --- |
| `POST` | `/api/mail/messages/send` | 发送邮件：前置校验 1）From 必须属于 sender 账户登记的 address / alias；2）出站速率是否超阈值（超阈值给 warning，不强制拒绝）→ 走 Mox WebAPI Send → 成功后触发该账户 IDLE 通知 |
| `GET` | `/api/mail/messages/{messageId}` | 获取已解析 message（**实时从 IMAP 拉**，不从索引读） |
| `GET` | `/api/mail/messages/{messageId}/raw` | 下载 raw message，二次确认 + 大小限制（≤ 50MB）。实时 IMAP |
| `GET` | `/api/mail/messages/{messageId}/parts/{partId}` | 下载附件/part。**实时从 IMAP 拉取并流式转发**——Phantom 不落地附件。 |
| `POST` | `/api/mail/messages/{messageId}/move` | 移动：实时 IMAP `MOVE` → 成功后同步到索引（由 IDLE 二次确认最终一致） |
| `POST` | `/api/mail/messages/{messageId}/flags` | 加 flag：实时 IMAP `STORE` |
| `DELETE` | `/api/mail/messages/{messageId}/flags/{flag}` | 删 flag |
| `DELETE` | `/api/mail/messages/{messageId}` | 删除：打 `\Deleted` flag（默认不 EXPUNGE，去 Trash） |

> **邮件级操作双向**：如果用户在 Thunderbird/手机客户端做了移动/删除/打标，imapsync 通过 IMAP IDLE 感知并同步回 Phantom 索引。系统级操作（账户/域名/别名/运行时）单向，严格 C3。

### 6.9 Mailbox / IMAP 浏览 API（新增，由 imapsync + search 提供）

| Method | Path | 说明 |
| --- | --- | --- |
| `GET` | `/api/mail/mailbox/accounts` | Mailbox 顶部账户选择器：账户 ID + 显示名 + 主地址 + 未读数 |
| `GET` | `/api/mail/mailbox/{account}/folders` | 该账户的文件夹树（含未读数 + 总邮件数）。**实时从 IMAP 拉**（不走缓存，保证未读数准确） |
| `GET` | `/api/mail/mailbox/{account}/{folder}/messages` | 邮件列表（`?page=&size=&sort=`，按时间倒序默认 size=100）。从 `mail_messages` 索引读（保证翻页性能），未读数/总计数从实时 IMAP 补。 |
| `GET` | `/api/mail/mailbox/messages/{id}` | 单封详情：从索引读头/preview；**正文实时从 IMAP 拉取**（保证最新，避免索引残留） |
| `GET` | `/api/mail/mailbox/messages/{id}/raw` | 下载 raw message（二次确认 + 50MB 上限）。实时 IMAP |
| `GET` | `/api/mail/mailbox/messages/{id}/parts/{partId}` | 流式下载附件。实时 IMAP，Phantom 不落地 |
| `POST` | `/api/mail/mailbox/messages/{id}/move` | 见 §6.8 |
| `POST` | `/api/mail/mailbox/messages/{id}/flags` | 见 §6.8 |
| `DELETE` | `/api/mail/mailbox/messages/{id}/flags/{flag}` | 见 §6.8 |
| `DELETE` | `/api/mail/mailbox/messages/{id}` | 见 §6.8 |
| `GET` | `/api/mail/search` | **全文搜索**：`q=关键词&scope=global|{account_id}&folder=&from=&to=&before=&after=&has_attachment=&flag=&page=&size=`。走 FTS5，返回命中 snippet + 高亮区间 |
| `POST` | `/api/mail/messages/save-draft` | 保存草稿：IMAP `APPEND` 到 Drafts 文件夹 |

关键性能保障：
- `messages` 列表走索引，不走 IMAP SEARCH（IMAP SEARCH 逐封扫描 10 万封邮件要几十秒）。
- folders 未读数实时 IMAP，但只请求 `STATUS (MESSAGES UNSEEN)`，不请求邮件内容。
- 搜索 `size` 默认 50，最大 200。

### 6.10 Webhook API（安全加固）

| Method | Path | 说明 |
| --- | --- | --- |
| `POST` | `/api/mail/mox-webhook/incoming` | 接收 Mox incoming webhook |
| `POST` | `/api/mail/mox-webhook/outgoing` | 接收 Mox outgoing delivery webhook |
| `GET` | `/api/mail/deliveries` | 投递事件摘要（分页 + 过滤） |
| `GET` | `/api/mail/deliveries/{id}` | 投递详情摘要 |

**硬性安全要求**：
- **签名算法**：HMAC-SHA256。Header：`X-Mox-Signature: sha256=<hex>`，签名内容 = `timestamp + "." + raw_body`。Header `X-Mox-Timestamp: <unix_seconds>`。
- **Replay 防护**：timestamp 与当前时间差 > 15 分钟 → 401 丢弃。
- **源地址限制**：只接受 `127.0.0.1` 和 `::1`（Mox 和 Phantom 同机）。unix socket 调用天然满足。
- **大小限制**：请求体 ≤ 1MB。
- **持久化策略**：只存 `message_id_hash`、`from/to_domain` 摘要、`subject_snippet` ≤ 80 字、event、SMTP code、错误摘要（redacted）、时间、关联 queue id。不存正文/HTML/附件。

### 6.11 Logs API（增强）

| Method | Path | 说明 |
| --- | --- | --- |
| `GET` | `/api/mail/logs/sources` | Mox 日志源列表（路径白名单内） |
| `GET` | `/api/mail/logs/tail` | bounded tail：默认最近 200 行或 256KB，硬上限 1000 行 / 1MB / 5s 超时 |
| `GET` | `/api/mail/logs/search` | bounded search：`?q=` 关键词，硬上限同上 |
| `GET` | `/api/mail/logs/stream` | SSE live tail：**采样 + 回压**。采样率档位 high/normal/low（默认 normal = 最多 30 行/秒）。消费不及时跳过非 error/warning 行，同时返回 `skipped_count`。 |

所有日志内容必须走全局 safelog + **Mail 专属 redaction 规则集**（§10.2）。服务 slog 只记录启动、退出、probe 失败和摘要，不记录完整 Mox stdout/stderr 原文。

### 6.12 Backup / Maintenance API（保留，不变）

---

## 7. 生命周期管理与及时发现问题

### 7.1 状态模型

```text
unconfigured → configured → starting → running
                                       ↓
                                     degraded
                                       ↓
               ← stopping ← failed ← stopped ←
                                    ↑
                                  unknown
```

补充字段：
- `config_drifted`（bool）：检测到外部修改标记后阻塞所有写 API。
- `mox_min_version_met`（bool）：告警用。
- `adopted`（bool）：从 marker 孤儿进程 adopt 的标志。

### 7.2 Supervisor 要求（独立 moxsupervisor）

不复用 `internal/supervisor`。独立实现以下 9 条：

1. **参数数组执行**：`exec.CommandContext(name, arg...)`，禁止 shell 字符串。
2. **独立 process group**：`cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}`。停止时先 `killpg` 整组。
3. **Marker 文件**（`<data_dir>/mail/mox/config/.marker.json`）schema：
   ```json
   {
     "marker_version": 1,
     "binary_path": "<abs>",
     "config_dir": "<abs>",
     "data_dir": "<abs>",
     "phantom_instance_id": "<uuid，Phantom 首次启动生成永久不变>",
     "boot_id": "<系统 boot id，/proc/sys/kernel/random/boot_id>",
     "pid": 12345,
     "pgid": 12345,
     "process_start_time_ns": 1234567890,
     "started_at": "<ISO8601>"
   }
   ```
4. **孤儿进程识别（Phantom 启动时执行）**：
   - marker 不存在 → 无管理中进程。
   - marker 存在且 PID 不存在 → 上次崩溃，清理 marker → `stopped`。
   - marker 存在且 PID 存在 → **四重校验**：
     a. `phantom_instance_id` 匹配。
     b. `boot_id` 匹配（PID wrap 跨 reboot 防护）。
     c. `/proc/<pid>/stat` 的 starttime 与 `process_start_time_ns` 匹配（同 reboot 内 PID wrap 防护）。
     d. `/proc/<pid>/cmdline` 第 0 个 token 与 `binary_path` 一致（同名进程防护）。
   - 全通过 → `adopted=true`，纳入管理（不重新 Start）。任一失败 → **绝不 kill 外部进程**，仅 UI 标 warning：「发现疑似外部 Mox 进程 (PID=xxx)，未纳入 Phantom 管理」。
5. **只管理 marker 匹配的 Mox**，永远不 kill 外部手工部署的 Mox。
6. **停止升级策略**：
   ```
   SIGTERM → wait 30s → 仍存活
     → SIGTERM 到进程组 → wait 10s → 仍存活
       → SIGKILL 到进程组 → wait 5s → 报错"进程无法终止"
   ```
7. **Start 前预检查清单**（全部通过才 `exec.Start`）：
   - 端口 preflight（§5.3）。
   - `mox config test` 通过。
   - 目录权限：config 可读 / data 可写（0700，owner=Mox 用户）/ cert 权限正确。
   - binary 存在且可执行 + 版本 ≥ 最低要求（告警不通过也可 Start，但 UI 标 warning）。
   - marker 如果存在 → 按第 4 条孤儿识别处理。
8. **启动成功 = readiness probe 通过**（不是 `exec.Start` 返回 nil）。
9. **退出检测与重启**：`cmd.Wait()` 后：
   - 写 `mail.mox.process.exited` event（exit_code / exit_signal / stderr 摘要 ≤ 500 字）。
   - `desired_enabled=false` → 标记 stopped + 清理 marker。
   - `desired_enabled=true` → 进入 §7.4 crash loop backoff。

### 7.3 Readiness 与 Health Probe（9 层 + 新增 reputation）

| 层 | 名称 | 检查内容 | Cadence |
|---|---|---|---|
| L1 | process | PID 存活、process group、/proc starttime 匹配 marker | 5 秒 |
| L2 | control | `mox config list`（或等价 CLI）正常返回 | 15 秒 |
| L3 | webapi | unix socket / loopback WebAPI intro 方法可达 | 15 秒 |
| L4 | smtp | 本机 TCP connect 到 25，读 banner（以 `220` 开头），不发 EHLO | 15 秒 |
| L5 | imap | 本机 TCP connect 到 993，TLS 握手成功，banner 校验 `* OK` | 15 秒 |
| L6 | dns | MX / SPF / DKIM / DMARC / TLS-RPT / PTR / TLSA / Autoconfig-SRV 检查 | 5 分钟或手动；首次 setup 和 domain 变更后立即 |
| L7 | delivery | 队列积压（>100=warning、>500=critical）；最近退信率（>20%=warning）；suppression 1 小时增长 >50=warning；webhook 失败 >3 重试 | 60 秒 |
| L8 | certificate | 所有证书存在 + 过期（<30d=warning，<7d=critical）；最近续签错误 | 60 秒 |
| L9 | reputation | **DNSBL 检查**：查 8 个主流 DNSBL（Spamhaus SBL/XBL/PBL、SpamCop、URIBL、SORBS、Google Safe Browsing DNSBL、UCEPROTECT，写死白名单）。命中 >0 = warning，≥3 = critical。只查 MX IP。 | 60 分钟，或投递失败率 >20% 立即触发 |

**Probe 调度规则**：
- 手动触发：同类型 probe 正在运行则 cancel 前一个；上次完成 < 10 秒直接返回缓存。
- 启动 readiness：L1-L5 每秒，最多 60 秒；超时 Start 失败。
- DNSBL 用 DNS 查询（反查 IP + DNSBL zone），不 HTTP API。
- 结果持久化到 `mail_mox_health_checks`，每层保留最近 100 条，1 小时后台任务清理超量。
- **SSRF 防护**：所有出检查的目标 host 固定为 `127.0.0.1` / `::1` / `已登记域名的 MX`（不允许用户自定义）。

### 7.4 Crash Loop 与 Backoff（明确数值）

```
连续失败次数 → 等待时间
1 → 2s
2 → 10s
3 → 30s
4 → 120s
≥5 → 进入 `failed` 状态，停止自动重启，要求 owner 介入
```
- 连续成功运行 ≥ 10 分钟 → 失败计数清零。
- UI 展示：最近 5 次 exit code、stderr 摘要、日志入口、配置回滚入口、「用已知好配置重启」按钮。
- slog 限频：每 60 秒最多 1 条 restart attempt failed，避免日志刷屏。

### 7.5 配置变更与回滚（10 步 apply 流水线）

所有系统级变更必须经过 `configapply` 模块的以下流水线。**UI 必须展示每步状态 + 进度百分比 + 当前步骤说明**，禁止返回空 response 的黑屏操作：

```
Step 1/10  [10%]  从 SQLite 期望状态渲染出新 mox.conf / domains.conf 到内存
                  → UI 显示字段变更列表（仅字段名，不含 secret）
Step 2/10  [20%]  写入 temp 文件 mox.conf.tmp-<uuid>、domains.conf.tmp-<uuid>，权限 0600
Step 3/10  [30%]  运行 `mox config test --config=... --domains=...`
                  失败 → FAIL，返回 test 输出全文（redacted）
Step 4/10  [40%]  计算新旧配置摘要 diff（字段名 + 1 行值摘要，不含 secret）
                  → 已在 Step 1 前端展示，这里做 server side 二次校验
Step 5/10  [50%]  写 audit pending event：{operator, what_fields_json, diff_summary}
Step 6/10  [60%]  备份当前 config：
                  mox.conf → mox.conf.bak-<timestamp>（保留最近 N 份，默认 20）
                  domains.conf 同理
Step 7/10  [70%]  原子 rename（文件系统原子操作）：
                  rename(temp, 正式路径) 按顺序 domains → mox
                  （dynamic 先写，防止 static 生效后 dynamic 没落地）
Step 8/10  [80%]  判断变更类型：
                  - 仅 domains.conf / 动态字段变更 → 等待 30s Mox 自动 reload
                  - mox.conf static / 证书 / 端口 / adminpasswd 变更 → graceful restart
Step 9/10  [90%]  readiness probe（L1-L5 全 ok，超时 60 秒）
                  失败 → ROLLBACK
Step 10/10 [100%] 更新 SQLite：last_applied_hash、last_applied_at
                  写 audit committed event
                  写 `mail.config.updated` SSE 推送
                  → SUCCESS，UI 绿色勾 + 可关闭

ROLLBACK（第 9 步失败时自动触发，不需要用户确认）：
  R1. rename bak-<timestamp> → 正式路径（再失败就标 critical）
  R2. 如果是 static 变更导致的 restart 失败 → restart 旧配置
  R3. readiness probe（旧配置）
  R4. 成功 → 标 `rollback_available` + audit rollback_completed + SSE
  R5. rollback 失败 → 标 `rollback_failed` critical
     + 保留所有中间文件（24 小时不清理）+ UI 红色 banner 要求人工处理

FAIL（第 3 步前失败）：
  - 仅保留 temp 文件（24 小时后自动清理）
  - 不触发回滚
  - 写 audit rejected event
```

UI 必须区分 5 种配置状态标志：
- `saved_not_applied`：已存 SQLite 但 apply 没跑或没成功 → 黄色
- `running_stale`：运行中但配置不是最新（待 restart/reload）→ 橙色
- `rollback_available`：存在上一份已验证配置 → 蓝色（展示回滚按钮）
- `rollback_failed`：回滚失败 → 红色 critical
- `config_drifted`：检测到外部修改 → 红色 banner + 阻塞写 API

### 7.6 可见性事件（不变）

§7.6 原事件类型不变（~30 条），补充 3 条：
- `mail.cert.renewed` / `mail.cert.renew_failed`
- `mail.reputation.dnsbl_hit`
- `mail.index.sync_error`

事件 payload 规则不变：只记稳定 ID、domain、account、queue id、duration、状态、redacted 错误摘要。不记密码/token/完整正文/附件/完整 subject/完整收件人。

### 7.7 UI 可见性要求（增强为 15 问）

Overview 首屏必须能回答：
1. Mox 是否真的在跑？
2. Phantom 是否希望它在跑？
3. 当前暴露了哪些 mail ports？每个是否可用？
4. WebAPI / IMAP / SMTP / control / process 各层 probe 是否 ok？
5. 最近一次 DNS check 什么时候？是否全通过？
6. 队列是否积压？积压的主要类型是什么？
7. 最近一次投递失败是什么类型（SMTP 5xx / 4xx / DNSBL / 超时）？建议下一步看哪里？
8. 证书是否接近过期？每张还剩几天？
9. 出站速率是否异常（超过阈值）？
10. IP 是否在 DNSBL 上？命中几个？
11. 最近一次配置是否已应用？是否 running_stale？
12. 是否检测到外部配置修改（drifted）？
13. 全文索引健康吗？有无账户同步失败？
14. 如果出了问题，下一步应该看哪里？（给出跳转建议）
15. 版本是否低于最低要求？

核心故障永远不要藏在 Logs 页面。Logs 是证据，Overview 和 inspector 展示诊断摘要。

### 7.8 出站速率监控 & 反滥用

- **指标**：`outbound_smtp_count_{1m,10m,1h,24h}`（从 delivery events + queue 统计，滑动窗口）。
- **阈值**（可调，默认）：1h > 200 封 = warning；1h > 1000 封 = critical；24h > 5000 封 = critical。
- **告警动作**：Overview 标变色 + SSE 推送 + 可选邮件（用 Mailbox 自己发 system notice）。
- **建议补救**：Overview 卡片列出 3 条建议：
  1. 检查最近登录日志（是否被盗）。
  2. 查看 suppression list 激增情况。
  3. 确认正常 → Settings 调大阈值（附二次确认：「我确认这是正常业务量」）。

### 7.9 全文索引健康监控

- 每个账户独立状态：idle / syncing / error / paused（disabled）。
- inspector 显示「同步中账户数 / 总账户数」。
- 最近 24h 无 IDLE 推送 → warning（连接挂了，imapsync 自动重连 + 指数退避）。
- FTS5 索引总大小：> Settings 阈值（默认 10GB）→ warning，自动降级为「只索引邮件头 + preview 200 字，不存 body_text」；UI 说明原因 + 提供「清理索引并重新同步」入口。

---

## 8. 前端页面功能清单

### 8.1 Overview（增强版）

- 状态条：running/degraded/failed/stopped，右侧带 `config_drifted` / `rollback_failed` / `version_low` 三个红色 badge。
- Desired vs Observed 双状态显示。
- PID、uptime、version（变色）、config hash、synced/drifted 标志。
- **6 个端口状态面板**：25/465/587/993/10444/10445 各自独立 status dot + 最新检查时间。
- **5 层 probe status dots**：process/control/webapi/smtp/imap 每层独立 + 最新错误摘要 tooltip。
- DNS health summary（8 项独立 dot：MX/SPF/DKIM/DMARC/TLS-RPT/PTR/TLSA/Autoconfig-SRV + MTA-STS 灰标）。
- Queue depth / deferred count / failed count（超阈值变色）。
- Last delivery failure：类型 tag + SMTP code + 80 字摘要 + 「查看 Logs / 重试队列」跳转按钮。
- 出站速率仪表盘：1m/1h/24h 三窗口 + 阈值线 + 超过百分比。
- DNSBL 命中卡片：命中数 + 命中列表（可点进 delist 链接）+ 「重新检查」按钮。
- 证书列表：每行 hostname + 倒计时天数（变色）+ 签发方式。
- 全文索引健康：同步进度条 + 失败账户数（非 0 则标红）。
- 配置状态标志：saved_not_applied / running_stale / rollback_available / drift 任一存在则显眼 banner。
- 操作按钮：Start、Stop、Restart、Probe、Open logs、Resolve drift（如有）。
- 右侧 inspector：按 §4 常驻信息。

### 8.2 Setup（增强，新增 binary install）

- **Mox Binary 区域**：
  - Detect 按钮 → 表格显示所有发现的 Mox（PATH + 受控目录 + version + is_managed + meets_min_version）。
  - 版本下拉（Phantom 支持的版本列表）+ Download 按钮 → 进度条 + checksum 校验 + 安装结果。
  - Uninstall 按钮（仅受控 binary）+ 二次确认。
- Instance mode：create new / import existing（read-only 模式有视觉标识 + 说明哪些功能不可用）。
- Config dir / data dir / public hostname。
- DNS provider：下拉（Cloudflare/DNSPod/Route53/手动）+ token 输入 + 连接测试按钮。
- Mox 运行用户（输入框，默认当前用户，建议 `_mox`）。
- WebAPI endpoint：自动填 unix socket 路径；不可用时填 127.0.0.1:10445。
- Mox Webmail 端口：默认 10444，监听地址（单选：127.0.0.1 / 0.0.0.0，切换到后者弹安全警告）。
- **Port binding preflight**：自动跑 6 个端口，结果逐项显示，permission_denied 下显示 3 条文本指引可复制。
- Initialize action：按步骤跑（mkdir → 默认 config → 生成 adminpasswd → apply → Start → readiness）。

### 8.3 Domains（增强，去掉 MTA-STS，加 TLSA + Autoconfig SRV）

- Domain list table：domain、enabled、synced、8 项 DNS status dots + MTA-STS 灰标、DKIM selector、最近 dnscheck 时间 + 状态。
- **DNS records copy panel**（点 domain 展开）：分 9 类（MX/SPF/DKIM/DMARC/TLS-RPT/PTR/TLSA/Autoconfig-SRV/TLSA），每类 1 键复制 + 状态指示（存在=绿/缺失=红/值不匹配=黄）。
- DNS check 动作：单域名 / 全域名按钮。
- Enable/disable/delete。
- DKIM：显示 selector + 公钥 TXT 值 + 1 键复制。私钥永远不可见。
- TLSA：显示当前 cert 的 `3 1 1` 值 + DNS 查询到的值 + 匹配状态。自动写入 DNS provider（如有 token）按钮 + 手动复制按钮。

### 8.4 Accounts（增强，新增同步进度）

- Account table：account、主地址、其他地址数、enabled、last login（时间+IP）、mailbox size、message count、**同步状态**（进度条 x/y 封 + status dot：idle/syncing/error/paused）、synced。
- 每行右侧操作：重置密码、启用/禁用、重新同步、删除。
- Create account 模态：用户名、所属域名、主地址、显示名、密码（自动生成 + 可改 + 强度指示）+ quota（可选）。
- Reset password：密码生成/自定义 + 一次性显示弹窗。
- Danger delete（MVP 默认禁用，只能 disable；hard delete 走 Settings 危险区）。

### 8.5 Aliases（不变，补充 catch-all 标识 + list 模式）

### 8.6 Delivery（增强，新增出站速率）

- Queue table：id、from_domain、to_domain、next_attempt、last_error（redacted）、hold、action buttons。
- Filters：account、sender domain、recipient domain、hold、next_attempt 范围。
- Actions：hold、unhold、schedule、fail、drop（后两个危险确认）。
- Retired queue（tab）。
- Suppression list（tab）+ 按地址搜索。
- Webhook queue（tab）。
- **Outbound rate panel**：仪表盘（3 窗口）+ 阈值线 + 告警历史 + 跳转调整阈值。

### 8.7 Mailbox（全新，非轻量，三栏布局）

顶部栏：
- 账户选择下拉（账户名 + 未读数 badge）。
- 全局搜索框（带 scope 切换：全局 / 当前账户 / 当前文件夹）+ 高级过滤按钮（from/to/时间/附件/flag）。
- Compose 按钮（蓝底，突出）。
- 搜索通知 dot（有未查看的投递失败）。

左栏（30%）：
- 文件夹树：INBOX / Starred / Sent / Drafts / All Mail / Archive / Trash / Spam / [用户自定义文件夹...]。
- 每个文件夹后标 `未读数 / 总邮件数`，未读 > 0 粗体。
- 右键菜单（或长按）：重命名、删除（非系统文件夹）、标记所有已读。
- 账户级操作下拉：新建文件夹、清理垃圾箱、清理已删除邮件。

中栏（40%）：
- 邮件列表（按 internal_date 倒序，分页 100 条/页，滚动到底部自动加载下一页）。
- 每行：是否有附件 dot、是否未读（粗体）、flagged 星标、from、subject（长文本截断）、date、preview 首行（灰色小字，1 行）。
- 支持复选框多选 → 批量：打标 / 移动 / 删除 / 标已读。
- **搜索结果模式**：显示命中的 scope + 关键词高亮 snippet。
- 右键菜单：回复 / 转发 / 移动 / 删除 / 打星 / 标已读。

右栏（30%，中栏点邮件后展开）：
- 顶部：subject（大字体）、from（带头像占位或首字母）、to + cc（可展开看全部）、date（精确到分钟）+ 「回复/回复全部/转发/更多」（移动/删除/打标）操作。
- 中部：HTML 渲染（**沙盒 iframe，禁用 JS + 表单 + 外部资源**；远程图片默认不加载，有「显示图片」按钮 + 提示风险）。
- 纯文本切换：如果是 multipart，支持在 HTML/纯文本间切换。
- 底部：附件列表，每项显示 文件名 + 大小 + MIME 类型图标 + 下载按钮（点击后流式下载，不经过 Phantom 落地）。

**Compose（独立模态窗）**：
- From 下拉：只能选当前账户已登记的 address / alias。
- To / Cc / Bcc：支持邮箱自动补全（从已发送邮件中累积的地址簿）。
- Subject。
- 富文本编辑器（推荐 trix，简洁够用）+ HTML / 纯文本切换。
- 附件上传：拖拽或按钮 → multipart 传给 Phantom → Phantom → Mox WebAPI。**Phantom 不落地附件**（multipart 流式转发）。附件数 / 总大小限制前端即时提示。
- 底部按钮：存草稿、发送（带发送前确认：附件数、收件人数、是否提醒「可能忘记加附件」）。
- 出站速率超阈值：黄色提示条「当前出站速率较高，可能影响 IP 声誉，是否继续发送？」

### 8.8 Search 结果页

如果从 Mailbox 搜索 → 在中栏显示结果；如果从全局 Dashboard 搜索 → 独立页面。

- 顶部：搜索 query + scope + 命中数 + 高级过滤栏。
- 过滤栏：scope（全局/账户）、folder、from、to、before/after、has_attachment、flag（已读/未读/已标星）。
- 结果列表：每行 account + folder + from + subject + date + snippet（关键词高亮，周围 80 字上下文）。
- 点结果 → 跳到 Mailbox 右栏显示对应邮件 + 中栏滚动定位到该邮件。

### 8.9 Logs（增强，新增 redaction indicator + 采样率控制）

- Source selector。
- Bounded tail（行数/字节可输入，硬上限显示）。
- Live tail over SSE：**采样率档位切换**（high/normal/low）+ `skipped_count` 实时显示。
- Search with max bytes/max lines。
- **Redaction indicator**：底部常驻黄色条 + icon「已启用隐私脱敏：邮箱地址/密码/完整 subject/Authorization 等敏感信息已自动替换」+ 可展开查看脱敏规则列表（§10.2）。
- 健康告警的「查看日志」按钮自动带过滤参数（时间范围 + 错误关键字）。

### 8.10 Certificates（全新）

- 证书列表 table：hostname、类型（mail/autoconfig 等）、Issuer、Not Before、Not After（**进度条 + 倒计时天数，<30 天黄 / <7 天红 / 过期红闪**）、SAN 数量、状态（ok/expiring_soon/expired/error + 错误摘要）、签发方式（Cloudflare/手动等）、上次续签时间 + 下次计划续签时间。
- 每行操作：手动续签、查看详情、重新生成 TLSA 建议。
- 手动模式的未完成续签：顶部黄色 banner「DNS-01 挑战等待中 → TXT 名：___ → TXT 值：___（1 键复制）→ 我已添加，继续」。
- **TLSA 面板**（独立 tab）：每个证书一行，显示当前 TLSA 记录值 + DNS 查询到的实际值 + 匹配状态 + 自动写入 DNS provider（如有 token）按钮。

### 8.11 Settings（增强，新增多个子面板）

**General**：
- Binary path + 版本 + Mox 用户。
- Managed instance dirs（config/data/logs/backups）。
- Start on Phantom launch 开关。
- WebAPI credentials（masked + rotate 按钮 + generate 新值一次性显示）。
- Webhook secret（同上）。
- Port / listener settings：Mox 6 个端口 + Webmail 监听地址。**改端口后必须重新跑 preflight，通过才可保存**。

**DNS Provider**：
- Provider 下拉 + token 输入（masked）+ 连接测试按钮。
- 说明：token 用于 ACME DNS-01 和可选的 TLSA 自动写入，不会用于其他 DNS 记录。

**Search & Index**：
- 全文索引开关（关闭后停所有 imapsync goroutine，保留历史索引但不更新）。
- 索引大小阈值 GB（默认 10）+ 当前大小显示。
- 保留年限（0 = 全部）。
- 清理索引按钮（删除 FTS5 + mail_messages + mail_message_bodies，删除后下一轮全量重建）+ 二次确认。
- 当前各账户同步状态表格。

**Delivery**：
- 出站阈值（1h / 24h，滑块 + 输入）。
- suppression 保留天数。
- Webhook 重试策略。

**Backup & Retention**：
- 备份策略（自动/手动）、自动频率（日/周/月）、保留份数。
- Log retention（天）。
- Event retention（天）。

**危险区（Danger Zone，折叠面板，默认关闭，红色边框）**：
- **Detach managed Mox**：转为 import/read-only 模式，不再管理进程 + 配置，但保留 UI 可见性 + 日志。二次确认。
- **Delete Phantom cache**：删除全文索引 + UI cache + 健康检查历史 + delivery events；**不动 Mox config/data**。
- **Re-apply all config**：从 SQLite 强制覆盖磁盘（drift 解决入口 2）。
- **Reset adminpasswd**：重新生成 Mox admin password（会重启 Mox，影响 WebAPI）。
- **真正删除 Mox data**：三级确认（3 个 checkbox：「我理解这会永久删除所有账户邮件」/「我已经做过备份」/「此操作不可撤销」）+ 输入主账户名 + 60 秒倒计时按钮 + 输入验证码；默认隐藏在危险区最底部，需要展开 2 层。

---

## 9. 存储模型草案（大幅扩展）

SQLite 存 Phantom 管理状态、IMAP 索引和 FTS5。
Mox config/data 仍是邮件本体的事实来源。

所有表使用项目现有风格：`TEXT` 时间（ISO8601）、JSON 字段以 `_json` 后缀。迁移全部 additive。

```sql
-- ===== 基础设置 =====
CREATE TABLE IF NOT EXISTS mail_mox_settings (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  enabled INTEGER NOT NULL DEFAULT 0,
  start_on_launch INTEGER NOT NULL DEFAULT 0,
  managed_instance INTEGER NOT NULL DEFAULT 1,
  binary_path TEXT NOT NULL DEFAULT '',
  binary_version TEXT NOT NULL DEFAULT '',
  min_required_version TEXT NOT NULL DEFAULT '',
  config_dir TEXT NOT NULL DEFAULT '',
  data_dir TEXT NOT NULL DEFAULT '',
  public_hostname TEXT NOT NULL DEFAULT '',
  webapi_endpoint TEXT NOT NULL DEFAULT '', -- uds 路径或 http://host:port
  webapi_username TEXT NOT NULL DEFAULT '',
  webapi_password_ciphertext TEXT NOT NULL DEFAULT '',
  webhook_secret_ciphertext TEXT NOT NULL DEFAULT '',
  mox_run_as_user TEXT NOT NULL DEFAULT '',
  webmail_port INTEGER NOT NULL DEFAULT 10444,
  webmail_public INTEGER NOT NULL DEFAULT 0,
  dns_provider TEXT NOT NULL DEFAULT '',
  dns_provider_credentials_ciphertext TEXT NOT NULL DEFAULT '',
  fts_enabled INTEGER NOT NULL DEFAULT 1,
  fts_max_size_gb INTEGER NOT NULL DEFAULT 10,
  fts_retain_years INTEGER NOT NULL DEFAULT 0,
  outbound_limit_1h INTEGER NOT NULL DEFAULT 200,
  outbound_limit_24h INTEGER NOT NULL DEFAULT 5000,
  log_retention_days INTEGER NOT NULL DEFAULT 90,
  event_retention_days INTEGER NOT NULL DEFAULT 90,
  backup_retention_count INTEGER NOT NULL DEFAULT 20,
  last_applied_hash TEXT NOT NULL DEFAULT '',
  last_applied_at TEXT NOT NULL DEFAULT '',
  phantom_instance_id TEXT NOT NULL DEFAULT '', -- 首次启动生成的 UUID，永久不变
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

-- ===== 运行时状态 =====
CREATE TABLE IF NOT EXISTS mail_mox_runtime_state (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  desired_state TEXT NOT NULL DEFAULT 'stopped',
  observed_state TEXT NOT NULL DEFAULT 'unknown',
  config_drifted INTEGER NOT NULL DEFAULT 0,
  pid INTEGER NOT NULL DEFAULT 0,
  process_group_id INTEGER NOT NULL DEFAULT 0,
  boot_id TEXT NOT NULL DEFAULT '',
  process_start_time_ns INTEGER NOT NULL DEFAULT 0, -- 用于 PID wrap 校验
  adopted INTEGER NOT NULL DEFAULT 0,
  version TEXT NOT NULL DEFAULT '',
  started_at TEXT NOT NULL DEFAULT '',
  last_seen_at TEXT NOT NULL DEFAULT '',
  last_exit_at TEXT NOT NULL DEFAULT '',
  last_error_summary TEXT NOT NULL DEFAULT '',
  health_level TEXT NOT NULL DEFAULT 'unknown',
  health_json TEXT NOT NULL DEFAULT '{}', -- 9 层 probe 结构
  crash_count INTEGER NOT NULL DEFAULT 0,
  next_restart_at TEXT NOT NULL DEFAULT '',
  outbound_1m INTEGER NOT NULL DEFAULT 0,
  outbound_1h INTEGER NOT NULL DEFAULT 0,
  outbound_24h INTEGER NOT NULL DEFAULT 0,
  updated_at TEXT NOT NULL
);

-- ===== 健康检查 =====
CREATE TABLE IF NOT EXISTS mail_mox_health_checks (
  id TEXT PRIMARY KEY,
  check_type TEXT NOT NULL, -- process/control/webapi/smtp/imap/dns/delivery/certificate/reputation
  status TEXT NOT NULL,     -- ok/warning/critical/unknown
  target TEXT NOT NULL DEFAULT '',
  summary TEXT NOT NULL DEFAULT '',
  payload_json TEXT NOT NULL DEFAULT '{}',
  checked_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_mail_health_type_time
  ON mail_mox_health_checks(check_type, checked_at DESC);
-- 保留策略：每种 check_type 最近 100 条；后台任务 1h 清理

-- ===== 投递事件 =====
CREATE TABLE IF NOT EXISTS mail_delivery_events (
  id TEXT PRIMARY KEY,
  direction TEXT NOT NULL, -- incoming/outgoing/webhook
  event_type TEXT NOT NULL, -- delivered/deferred/failed/bounced/expired
  account TEXT NOT NULL DEFAULT '',
  from_domain TEXT NOT NULL DEFAULT '',
  to_domain TEXT NOT NULL DEFAULT '',
  message_id_hash TEXT NOT NULL DEFAULT '', -- SHA256(Message-Id)，去重用
  queue_msg_id TEXT NOT NULL DEFAULT '',
  smtp_code TEXT NOT NULL DEFAULT '',
  error_summary TEXT NOT NULL DEFAULT '', -- redacted
  subject_snippet TEXT NOT NULL DEFAULT '', -- ≤ 80 字，redacted
  payload_json TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_mail_delivery_time
  ON mail_delivery_events(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_mail_delivery_account_time
  ON mail_delivery_events(account, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_mail_delivery_dir_type_time
  ON mail_delivery_events(direction, event_type, created_at DESC);
-- 保留策略：90 天；Settings 可调

-- ===== 域名（Phantom 为来源）=====
CREATE TABLE IF NOT EXISTS mail_domains (
  id TEXT PRIMARY KEY,
  domain TEXT NOT NULL UNIQUE,
  enabled INTEGER NOT NULL DEFAULT 1,
  dkim_selector TEXT NOT NULL DEFAULT '',
  dkim_public_key TEXT NOT NULL DEFAULT '',
  tlsa_record_value TEXT NOT NULL DEFAULT '', -- 3 1 1 + SHA256
  mx_priority INTEGER NOT NULL DEFAULT 10,
  dmarc_policy TEXT NOT NULL DEFAULT 'p=quarantine',
  tlsrpt_address TEXT NOT NULL DEFAULT '',
  last_dnscheck_at TEXT NOT NULL DEFAULT '',
  last_dnscheck_status TEXT NOT NULL DEFAULT 'unknown',
  synced INTEGER NOT NULL DEFAULT 0, -- 是否已同步到 Mox config 并 apply 成功
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

-- ===== 账户 =====
CREATE TABLE IF NOT EXISTS mail_accounts (
  id TEXT PRIMARY KEY,
  account_name TEXT NOT NULL,
  domain_id TEXT NOT NULL REFERENCES mail_domains(id) ON DELETE CASCADE,
  enabled INTEGER NOT NULL DEFAULT 1,
  storage_quota_mb INTEGER NOT NULL DEFAULT 0, -- 0 = 无限
  display_name TEXT NOT NULL DEFAULT '',
  last_login_at TEXT NOT NULL DEFAULT '',
  last_login_ip TEXT NOT NULL DEFAULT '',
  mailbox_size_bytes INTEGER NOT NULL DEFAULT 0,
  message_count INTEGER NOT NULL DEFAULT 0,
  -- IMAP 同步状态
  sync_status TEXT NOT NULL DEFAULT 'paused', -- idle/syncing/error/paused
  sync_progress_total INTEGER NOT NULL DEFAULT 0,
  sync_progress_done INTEGER NOT NULL DEFAULT 0,
  sync_error_summary TEXT NOT NULL DEFAULT '',
  sync_json TEXT NOT NULL DEFAULT '{}', -- 每个 folder 的 uidvalidity、last_uid、last_internaldate
  synced INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  UNIQUE(account_name, domain_id)
);
CREATE INDEX IF NOT EXISTS idx_mail_accounts_domain ON mail_accounts(domain_id);

-- ===== 地址（一个账户多个 @domain）=====
CREATE TABLE IF NOT EXISTS mail_addresses (
  id TEXT PRIMARY KEY,
  account_id TEXT NOT NULL REFERENCES mail_accounts(id) ON DELETE CASCADE,
  address_local TEXT NOT NULL,
  address_domain TEXT NOT NULL,
  is_primary INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  UNIQUE(address_local, address_domain)
);
CREATE INDEX IF NOT EXISTS idx_mail_addresses_account ON mail_addresses(account_id);

-- ===== 别名 =====
CREATE TABLE IF NOT EXISTS mail_aliases (
  id TEXT PRIMARY KEY,
  domain_id TEXT NOT NULL REFERENCES mail_domains(id) ON DELETE CASCADE,
  alias_local TEXT NOT NULL, -- 空字符串 = catch-all
  is_catch_all INTEGER NOT NULL DEFAULT 0,
  list_mode TEXT NOT NULL DEFAULT '', -- ''/forward-only/list（开放所有人发）
  enabled INTEGER NOT NULL DEFAULT 1,
  comment TEXT NOT NULL DEFAULT '',
  synced INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  UNIQUE(domain_id, alias_local)
);
CREATE INDEX IF NOT EXISTS idx_mail_aliases_domain ON mail_aliases(domain_id);

CREATE TABLE IF NOT EXISTS mail_alias_recipients (
  id TEXT PRIMARY KEY,
  alias_id TEXT NOT NULL REFERENCES mail_aliases(id) ON DELETE CASCADE,
  recipient_address TEXT NOT NULL,
  created_at TEXT NOT NULL,
  UNIQUE(alias_id, recipient_address)
);
CREATE INDEX IF NOT EXISTS idx_mail_alias_recipients_alias
  ON mail_alias_recipients(alias_id);

-- ===== 证书 =====
CREATE TABLE IF NOT EXISTS mail_certificates (
  id TEXT PRIMARY KEY,
  hostname TEXT NOT NULL,
  not_before TEXT NOT NULL,
  not_after TEXT NOT NULL,
  issuer TEXT NOT NULL,
  serial_hex TEXT NOT NULL,
  san_dns_json TEXT NOT NULL DEFAULT '[]',
  pubkey_sha256_hex TEXT NOT NULL DEFAULT '', -- TLSA 计算用
  status TEXT NOT NULL DEFAULT 'ok', -- ok/expiring_soon/expired/error
  last_renewed_at TEXT NOT NULL,
  next_renew_at TEXT NOT NULL,
  last_renew_error TEXT NOT NULL DEFAULT '',
  renew_mode TEXT NOT NULL DEFAULT '', -- dns_cloudflare/dns_dnspod/dns_route53/manual
  renew_state_json TEXT NOT NULL DEFAULT '{}', -- 手动模式下存 pending challenge 等
  updated_at TEXT NOT NULL,
  UNIQUE(hostname)
);

-- ===== 邮件索引（IMAP 同步）=====
-- 只存邮件头 + 正文纯文本，不存 MIME/附件/内联图片/HTML。
CREATE TABLE IF NOT EXISTS mail_messages (
  id TEXT PRIMARY KEY, -- 内部 ID：SHA256(account_id || '|' || folder || '|' || uidvalidity || '|' || uid)
  account_id TEXT NOT NULL REFERENCES mail_accounts(id) ON DELETE CASCADE,
  folder TEXT NOT NULL, -- UTF-7 解码后的 display name
  uid INTEGER NOT NULL, -- IMAP UID
  uid_validity INTEGER NOT NULL, -- IMAP UIDVALIDITY
  message_id_header TEXT NOT NULL DEFAULT '', -- Message-Id header，用于线程关联
  -- 邮件头
  subject TEXT NOT NULL DEFAULT '',
  from_addr TEXT NOT NULL DEFAULT '',   -- 显示名 <email>
  from_mailbox TEXT NOT NULL DEFAULT '', -- 只存 email 部分，搜索去重用
  to_addrs TEXT NOT NULL DEFAULT '',
  cc_addrs TEXT NOT NULL DEFAULT '',
  bcc_addrs TEXT NOT NULL DEFAULT '',
  reply_to TEXT NOT NULL DEFAULT '',
  date TEXT NOT NULL,              -- Date header ISO8601
  internal_date TEXT NOT NULL,     -- IMAP INTERNALDATE ISO8601（排序主依据）
  size_bytes INTEGER NOT NULL DEFAULT 0,
  -- 附件元数据（不含内容）
  has_attachment INTEGER NOT NULL DEFAULT 0,
  attachments_json TEXT NOT NULL DEFAULT '[]', -- [{partId, filename, size, mime, sha256}]
  -- flags
  flags_json TEXT NOT NULL DEFAULT '[]', -- ["\\Seen", "\\Flagged", "\\Answered", ...]
  -- 正文预览（列表展示）
  preview TEXT NOT NULL DEFAULT '', -- 纯文本前 200 字
  thread_id TEXT NOT NULL DEFAULT '', -- 可选：按 subject+references 聚合
  synced_at TEXT NOT NULL,
  UNIQUE(account_id, folder, uid, uid_validity)
);
CREATE INDEX IF NOT EXISTS idx_mail_msg_account_folder_date
  ON mail_messages(account_id, folder, internal_date DESC);
CREATE INDEX IF NOT EXISTS idx_mail_msg_account_date
  ON mail_messages(account_id, internal_date DESC);
CREATE INDEX IF NOT EXISTS idx_mail_msg_account_from
  ON mail_messages(account_id, from_mailbox);
CREATE INDEX IF NOT EXISTS idx_mail_msg_account_date_flag
  ON mail_messages(account_id, internal_date DESC); -- flags 放 JSON 过滤

-- 正文纯文本（独立表，避免列表 SELECT 扫大 BLOB）
CREATE TABLE IF NOT EXISTS mail_message_bodies (
  message_id TEXT PRIMARY KEY REFERENCES mail_messages(id) ON DELETE CASCADE,
  body_text TEXT NOT NULL -- HTML 抽取后的纯文本（去掉 tag、URL 保留、附件文本不包含）
);

-- ===== 全文搜索 FTS5 =====
-- 使用 contentless FTS5（content=''），外部自行维护 insert/delete 同步。
-- content_rowid 用 mail_messages.id 的整数表示（取 SHA256 前 8 字节 as uint64，
-- 需封装保证唯一映射；或改用 rowid 主键的 mail_messages 表，实现时决定）。
CREATE VIRTUAL TABLE IF NOT EXISTS mail_fts USING fts5(
  subject,
  from_addr,
  to_addrs,
  body_text,
  tokenize = 'unicode61 remove_diacritics 2',
  content = '',
  contentless_delete = 1
);

-- ===== 配置变更审计 =====
CREATE TABLE IF NOT EXISTS mail_config_audits (
  id TEXT PRIMARY KEY,
  operator TEXT NOT NULL, -- Phantom 本地账号
  operation_type TEXT NOT NULL, -- apply/rollback/reapply/import_from_disk/binary_install/...
  fields_json TEXT NOT NULL DEFAULT '[]', -- 变更字段名列表（不含值）
  diff_summary TEXT NOT NULL DEFAULT '', -- 1 行摘要
  before_hash TEXT NOT NULL DEFAULT '',
  after_hash TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'pending', -- pending/committed/rejected/rollback
  error_summary TEXT NOT NULL DEFAULT '',
  duration_ms INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_mail_config_audit_time
  ON mail_config_audits(created_at DESC);
```

补充说明：
- 邮件级数据以 Mox data 为准；`mail_messages` 是索引副本，随时可删除重建（resync）。
- `mail_messages` + `mail_message_bodies` + `mail_fts` **可以独立在单独的 SQLite 数据库文件**中（`<data_dir>/mail/index/mail-fts.db`），方便用户一键关闭搜索功能时整库删除而不影响主 SQLite。
- FTS5 总大小超阈值（默认 10GB）→ imapsync 只写 preview 不再写 `mail_message_bodies`，UI 提示。

---

## 10. 安全、日志与审计（加固版）

### 10.1 Secret 边界（不变，补充清单）

| 敏感项 | 保存位置 | 展示规则 | 传输规则 | Audit 规则 |
|---|---|---|---|---|
| Mox admin password | 文件系统 `adminpasswd`（Mox 管理） + keywrap 加密备份在 SQLite | UI 不可见；rotate 时一次性展示 | 仅 stdin / 文件 | 只记 rotate 操作，不记值 |
| 账户密码 | Mox 自己的 passwd 文件 | 生成/重置时一次性展示 | stdin 传给 `mox setaccountpassword` | 只记 account + 是否 generated |
| WebAPI password | `internal/keywrap` 加密存 SQLite | masked `***` | Basic Auth，走 uds 或 loopback | 只记 rotate 操作 |
| Webhook secret | 同上 | masked | HMAC-SHA256 签名计算用 | 同上 |
| DNS provider token | 同上 | masked | HTTPS 调 provider API | 只记 connect test 结果 |
| DKIM 私钥 | 文件系统，权限 0600 | **永远不可见** | 不进 Phantom 内存（由 Mox 使用） | 禁止任何审计记录 |
| TLS 私钥 | 文件系统，权限 0600 | 永远不可见 | certmanager 写文件时直接从内存写 | 禁止任何审计记录 |
| 邮件正文 / HTML / 附件 | Mox data 目录 | 仅 Mailbox 页面（登录态下访问本人邮件） | 实时 IMAP 拉取，Phantom 不落地 | **禁止任何审计/事件/slog 记录完整正文** |
| 完整 subject | — | 事件/slog 中只允许 ≤ 80 字 snippet + redaction | — | 同上 |

### 10.2 日志（新增 Mail 专属 redaction）

**Mail 专属 redaction 规则**（叠加全局 safelog 之上，对 Mox 原始日志流逐行应用）：

| Redaction 项 | 匹配模式 | 替换为 |
|---|---|---|
| 完整邮箱地址 | `\b[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}\b` | `***@***.***` |
| 密码/token/Authorization | `(?i)(password|token|authorization|auth|secret)\s*[:=]\s*\S+` | `$1=***` |
| SMTP MAIL FROM | `MAIL FROM:\s*<[^>]*>` | `MAIL FROM:<***>` |
| SMTP RCPT TO | `RCPT TO:\s*<[^>]*>` | `RCPT TO:<***>` |
| Subject 超长截断 | `(?i)^Subject:\s*.{81,}$` | 前 80 字符 + `…` |
| Received 内网 IP | `(?i)Received: from\s+\d{1,3}(\.\d{1,3}){3}` | `Received: from ***` |
| DKIM-Signature b= 值 | `b=[A-Za-z0-9+/=\-]+` | `b=***` |

Phantom Lancer 全局 service slog（`internal/logging`）只记录：
- Mox 启动/停止/退出摘要（限频 60 秒）。
- health probe **状态变化**（不是每轮都记）。
- 配置校验失败（字段名，不含值）。
- webhook 处理失败摘要。
- DNS check 失败（域名 + 检查项，不含具体值）。
- 证书续签结果。

**禁止**进入 slog：完整邮件正文、raw message、完整 MIME、完整 Mox stdout/stderr 原文（逐行读 log 文件的 API 也算）、密码/token/cookie/Authorization、完整 subject、完整收件人列表、DKIM 私钥材料。

Mox 原始日志通过 Logs 模块受控读取的硬性约束：
- 路径白名单（`<data_dir>/mail/mox/logs/` + Settings 中手动添加的绝对路径，二次确认）。
- 最大行数 1000 / 最大字节 1MB / 超时 5s。
- **应用 Mail redaction 后再输出**（即使是用 `logs/tail` 也一样）。
- SSE live tail 采样 + 回压（§6.11）。

### 10.3 审计（补充操作清单）

§10.3 原必须写 audit 的操作不变，补充：
- Binary 下载 / 安装 / 卸载。
- DNS provider token 更新 / 连接测试。
- 证书手动续签 / 自动续签结果。
- TLSA 记录自动写入 DNS provider。
- 全文索引开关切换 / 清理索引。
- 解决配置漂移（`resolve-drift` API，两种 action 都要记）。
- 出站速率阈值调整。
- 真正的 Mox data 硬删除。
- import 外部实例（read-only 模式切换）。

审计 payload 规则（不变）：只包含对象 ID、操作类型、结果、redacted 错误摘要、duration_ms、风险等级。

### 10.4 SSRF 防护（新增）

所有触发 Phantom 出站查询的接口：
- `/api/mail/domains/{domain}/dns-check` → `domain` 必须 ∈ `mail_domains` 表，否则 403。
- DNSBL 检查 → 只查 `mail_domains` 解析到的 MX IP，不接受用户自定义 host/IP。
- Probe 的 connect 目标 → 固定 `127.0.0.1` / `::1` / Mox 配置中已登记的 hostname（且解析后必须为本机回环地址或本机公网 IP）。
- 出站 DNS 查询统一走系统 resolver，不允许用户自定义 DNS 服务器。

### 10.5 Webhook 安全（已固化，见 §6.10）

### 10.6 WebAPI 访问（新增，unix socket 优先）

- **推荐**：Mox WebAPI 通过 unix socket `<data_dir>/mail/mox/runtime/webapi.sock` 连接，权限 0600（Mox 用户与 Phantom 用户同组，或 Phantom 用 setgid 访问）。
- **Fallback**：`http://127.0.0.1:10445`。**禁止配置到 0.0.0.0**，Settings 强行改为公网地址时弹警告 + 二次确认 + 审计记录。
- 每次调用都附带 Basic Auth（用户名/密码从 keywrap 解密取）。
- 全局超时 30 秒。
- 返回体 401/403 时写 `mail.webapi.auth_failed` 事件 + 告警。

---

## 11. 兼容性与完整性（补充）

- Migration 全部 additive，不破坏已有 SQLite，不影响其他模块。
- 导入 read-only 模式不覆盖任何文件。
- 所有 config update 有备份 + 回滚，启动失败不删 data。
- 删除 account/domain MVP 只 disable，hard delete 必须走 Settings 危险区的三级确认。
- 更新 Phantom Lancer **不隐式升级 Mox binary**（约束 C4）。
- Mox binary 升级由用户手动完成：下载新版 Phantom（内置新版 checksum）→ Setup 页点 Download 指定新版本 → 自动 checksum + 安装 → 检测到 binary 变了 → 自动跑 config test + readiness + audit。
- Webhook 重复投递按 webhook id 幂等处理。
- 断线后前端通过 events SSE + runtime status 自动恢复。
- 卸载 Mail 模块的默认行为（如果未来要做）：删 FTS5 索引 + 运行时快照 + 停止 Mox；**不动** Mox config/data/certs/backups。

---

## 12. 分阶段实现建议（重新拆分，新增深度）

| 阶段 | 内容 | 对应功能 |
|---|---|---|
| **P0** | 文档与骨架 + 全量存储 schema 迁移 + Mail 导航占位 + API 骨架 + 事件常量 + 审计表 | 不执行真实 Mox 操作 |
| **P1** | moxbinary + 独立 moxsupervisor + marker 流程 + PID wrap 防护 + orphan adopt + crash loop backoff + L1-L3 probe | binary detect/download/install/uninstall、start/stop/restart、运行状态、L1-L3 health |
| **P2** | moxcli 封装 + configapply 10 步流水线（含 SSE 进度 + 自动回滚）+ drift 检测 + resolve-drift + Domain API + L4-L6 probe + DNS SSRF 防护 | 配置可视化生效（不黑屏）、domain CRUD、DNS records/check |
| **P3** | certmanager（ACME DNS-01 + 3 provider + 手动模式）+ 证书原子替换 + Mox reload + Certificates 页 + L8 probe + TLSA 生成/检查 | 证书签发/续签/告警 |
| **P4** | Accounts + Addresses + Aliases + Password 流程（stdin 传密码 + 强度校验）+ 全流程 audit + import/read-only 模式 | 账户/别名/密码管理 |
| **P5** | Queue + Delivery + Webhook（HMAC-SHA256 + 15min replay + loopback-only + 1MB limit）+ outbound rate 指标 + L7(delivery) probe + L9(reputation / DNSBL) | 队列、suppression、webhook、投递事件、DNSBL、出站速率 |
| **P6** | imapsync 模块（IMAP IDLE + UID 对齐 + 纯文本提取）+ FTS5 维护（insert/delete 同步）+ 索引健康监控（大小阈值降级） | 后台同步（无 UI 可见性优化） |
| **P7** | Mailbox 3 栏 UI + 文件夹列表（IMAP STATUS）+ 邮件列表（索引）+ 单封详情（实时 IMAP）+ 附件流式下载 + 邮件级操作（move/flag/delete）双向同步 + Compose（含草稿保存） | 完整邮件浏览 + 发送 |
| **P8** | 全文搜索（全局/单账户 + 过滤 + snippet 高亮 + 搜索结果页）+ 批量操作（移动/删除/打标） | 搜索 |
| **P9** | Mail 专属 redaction 全面生效 + Logs SSE 采样 + 回压 + backup/restore/verifydata + hard delete 危险区 + 留存策略后台任务（自动清理 90 天前 events/health_checks） | 安全/合规/运维收尾 |

P6（反向代理 Mox 原生 webmail 的决策分支）已由约束 C2 排除，P6/P7/P8 按「IMAP Sync + FTS5 + 自研 Mailbox UI」路线执行。

---

## 13. 已决策的开放问题（不再开放）

| # | 原问题 | 决策 |
|---|---|---|
| 1 | Mox binary 是否由 Phantom 下载 / owner 手工安装 / release bundle 附带 | 仅下载 + 安装（官方 release + 内置 checksum）；不自动升级；不随 Phantom bundle。 |
| 2 | 是否自动配置 capability/systemd service（低端口） | 不做。仅 UI 输出 3 条文本指引（capability/authbind/iptables DNAT）。 |
| 3 | 完整 Webmail 是否进入边界 | 是。走 IMAP 增量同步 + FTS5 + 自研 3 栏 Mailbox UI（约束 C2 排除了反向代理路径）。 |
| 4 | 是否需要 DNS provider API 自动写记录 | 仅 ACME DNS-01 的 `_acme-challenge` TXT 和 TLSA 记录；其他 A/MX/AAAA/SPF/DKIM/DMARC 一律只输出建议值，不自动写。 |
| 5 | 是否需要公网可达性检查 | MVP 不做，本机 L4 probe 足够。未来可选 P9.5：加第三方 can-you-connect-to-me API（非必需）。 |
| 6 | 是否需要邮件正文索引 | 需要，但仅正文纯文本 + 邮件头 + 附件元数据。不存 MIME/附件/图片。Settings 可一键关闭。 |

---

**END OF WIP DOC.**
