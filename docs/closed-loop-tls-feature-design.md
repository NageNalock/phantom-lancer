# 闭环 TLS 能力设计

## 1. 背景

Phantom Lancer 的部署目标是个人服务器 Web 控制台：单 binary、SQLite、SSE、个人单机部署和受控权限边界。当前服务已经支持 Go HTTP Server 直接监听端口，并支持通过运行期设置热切 `http_addr`，但 HTTPS 仍依赖外部反向代理或部署者手动处理。

Docker Registry、Codex Gateway、Images 私密图库和普通控制台登录一旦暴露到公网，都需要 HTTPS。为了不强制引入 Nginx、Caddy 或其他外部组件，本方案在 Phantom Lancer 主进程内提供闭环 TLS 能力：

- 服务无 TLS 配置也能启动，默认 HTTP bootstrap。
- Owner 可在 Web 页面配置 HTTPS listen address、证书文件路径和私钥文件路径。
- 保存前校验证书、私钥和端口绑定，失败不影响当前 listener。
- 保存成功后热切到 HTTPS，不要求进程重启。
- 证书文件续期后自动加载新证书，避免依赖外部 reload 命令。
- Registry 的 `/v2/*` 与 Web/API/SSE 共用同一个 HTTPS listener。

## 2. 目标与非目标

### 2.1 目标

- 支持 `HTTP` 与 `HTTPS` 两种主服务监听模式。
- TLS 配置可通过页面/API 写入 SQLite runtime settings，不要求修改 TOML 配置文件。
- 初次启动没有证书路径时仍可进入控制台，完成白屏配置。
- 支持在 `0.0.0.0:8443` 这类非 443 端口上直接提供 HTTPS。
- 支持使用已有 Let's Encrypt 证书文件。
- 支持证书文件内容热加载：路径不变时，续期后自动生效。
- 支持从 HTTP 切到 HTTPS、从 HTTPS 换端口、从 HTTPS 换证书路径。
- 所有危险变更写 audit，且不记录私钥内容。

### 2.2 非目标

- 不内置 ACME/Let's Encrypt 申请和续期流程。证书申请仍由部署环境完成。
- 不把证书私钥内容上传或保存到 SQLite。
- 不管理 DNS、Cloudflare、系统防火墙或云安全组。
- 不做多 listener 同时长期服务。第一版只保留一个当前 active listener。
- 不自动把 80/443 从其他服务迁移给 Phantom Lancer。

## 3. 用户场景

### 3.1 首次白屏配置 HTTPS

1. 服务以默认 HTTP 启动，例如 `127.0.0.1:8080` 或部署配置中的 `0.0.0.0:10443`。
2. Owner 通过本机、内网或 SSH tunnel 打开控制台。
3. 在 `设置 > 服务配置 > HTTPS` 中填写：
   - Listen address: `0.0.0.0:8443`
   - TLS enabled: `on`
   - Cert file: `/var/lib/phantom-lancer/tls/fullchain.pem`
   - Key file: `/var/lib/phantom-lancer/tls/privkey.pem`
4. 后端先校验证书和端口绑定。
5. 校验成功后切换 listener，页面提示跳转到 `https://<host>:8443/`。
6. 如果启用了 Secure Cookie，当前 HTTP session 可能需要重新登录。

### 3.2 证书续期

1. certbot 或其他工具续期证书。
2. deploy hook 把新证书复制到 Phantom Lancer 可读路径，保持文件路径不变。
3. Phantom Lancer 的 TLS certificate reloader 检测 mtime/size 变化。
4. 下一次 TLS handshake 使用新证书。
5. 如果新证书加载失败，继续使用上一份 last known good certificate，并记录脱敏服务日志。

### 3.3 Docker Registry 公网推送

1. 主服务 HTTPS endpoint 配为 `https://registry.example.com:8443`。
2. Docker Registry `Public URL` 填同样地址，`Require TLS` 开启。
3. Docker client 使用 host form：

```bash
docker login registry.example.com:8443
docker tag app:latest registry.example.com:8443/personal/app:latest
docker push registry.example.com:8443/personal/app:latest
```

Registry Public URL 可带 `https://`，但 Docker image reference 不带 scheme。

## 4. 信息架构

闭环 TLS 是全局服务暴露能力，应放在通用 `设置`，不放进 Docker、V2Ray 或 Images 模块。

建议页面结构：

- `设置 > 运行设置`
  - 允许根目录
  - Secure Cookie
- `设置 > 服务配置`
  - 当前 listener 状态
  - Listen address
  - TLS enabled
  - Cert file
  - Key file
  - Certificate status
  - Apply / Test

UI 风格保持 Quiet Agent Workbench：

- 不做大面积 onboarding 或营销式 HTTPS 引导。
- TLS 状态使用低噪音 pill：`http`、`https`、`cert expiring`、`cert invalid`。
- 私钥路径只显示路径，不显示文件内容。
- 危险操作使用二次确认：从 HTTPS 降级到 HTTP、切换公网监听地址、启用 `0.0.0.0`。

## 5. 数据模型

扩展 `RuntimeSettings`：

```go
type RuntimeSettings struct {
    AllowedRoots []string `json:"allowedRoots"`
    CookieSecure bool     `json:"cookieSecure"`
    Addr         string   `json:"addr"`

    TLSEnabled  bool   `json:"tlsEnabled"`
    TLSCertFile string `json:"tlsCertFile"`
    TLSKeyFile  string `json:"tlsKeyFile"`

    UpdatedAt string `json:"updatedAt,omitempty"`
}
```

SQLite settings keys：

```text
http_addr
http_tls_enabled
http_tls_cert_file
http_tls_key_file
cookie_secure
allowed_roots
```

兼容规则：

- 缺少 TLS keys 时按 `TLSEnabled=false` 处理。
- `tls_enabled=false` 时 cert/key 可为空，服务按 HTTP 启动。
- `tls_enabled=true` 时 cert/key 必须非空并通过校验。
- TOML 中不要求新增 TLS 字段；后续可选支持 env 作为 bootstrap 默认值，但不是必须路径。

## 6. 后端设计

### 6.1 Listener 配置结构

新增内部结构：

```go
type EndpointConfig struct {
    Addr        string
    TLSEnabled  bool
    TLSCertFile string
    TLSKeyFile  string
}
```

`httpserver.Manager` 从当前 `addr` 单值管理扩展为 `EndpointConfig` 管理：

```go
func New(initial EndpointConfig, handler http.Handler, log *slog.Logger) *Manager
func (m *Manager) Start() error
func (m *Manager) SwapEndpoint(next EndpointConfig) error
func (m *Manager) Endpoint() EndpointConfig
```

现有 `SwapAddr` 可保留为兼容 wrapper，内部调用 `SwapEndpoint`。

### 6.2 启动流程

启动时：

1. `config.Load` 只提供 bootstrap addr、data dir、DB path 等最小配置。
2. `EnsureRuntimeSettings` 写入默认 `http_addr`，不要求 TLS keys。
3. `GetRuntimeSettings` 读取 SQLite。
4. 如果 `TLSEnabled=false`，启动 HTTP listener。
5. 如果 `TLSEnabled=true`，先校验证书。
6. TLS 校验失败时，启动应失败还是降级 HTTP 需要明确：
   - 推荐：如果 TLS 是已保存的运行期配置，启动失败并写清楚错误，避免用户误以为公网 HTTPS 可用。
   - 可选 fallback：提供 `PL_TLS_BOOT_FALLBACK_HTTP=true` 只用于救援模式，默认关闭。

### 6.3 TLS listener

不要使用 `http.Server.ServeTLS` 直接读取固定证书文件。推荐显式构建 `tls.Config`：

```go
tlsConfig := &tls.Config{
    MinVersion:     tls.VersionTLS12,
    GetCertificate: certReloader.GetCertificate,
}
tlsListener := tls.NewListener(tcpListener, tlsConfig)
server.Serve(tlsListener)
```

这样证书续期后可以由 `certReloader` 动态加载。

### 6.4 CertificateReloader

职责：

- 启动或切换前使用 `tls.LoadX509KeyPair(certFile, keyFile)` 校验一次。
- 保存 last known good certificate。
- 每次 `GetCertificate` 时按短窗口检查文件 mtime/size，例如最多每 10 秒 stat 一次。
- 文件变化后重新加载 cert/key。
- 新证书加载失败时继续返回旧证书，并限频记录 warning。
- 不在日志里记录私钥内容、证书 PEM、完整错误堆栈。

状态字段：

```go
type TLSStatus struct {
    Enabled       bool   `json:"enabled"`
    Active        bool   `json:"active"`
    CertFile      string `json:"certFile,omitempty"`
    KeyFile       string `json:"keyFile,omitempty"`
    Subject       string `json:"subject,omitempty"`
    DNSNames      []string `json:"dnsNames,omitempty"`
    NotBefore     string `json:"notBefore,omitempty"`
    NotAfter      string `json:"notAfter,omitempty"`
    DaysRemaining int    `json:"daysRemaining,omitempty"`
    LastReloadAt  string `json:"lastReloadAt,omitempty"`
    LastError     string `json:"lastError,omitempty"`
}
```

### 6.5 保存与热切流程

新增接口：

```text
GET  /api/settings/listener
POST /api/settings/listener/test
POST /api/settings/listener
```

`POST /api/settings/listener/test`：

- 只校验，不持久化，不切换。
- 校验 addr 格式和端口范围。
- 尝试 bind 新 addr；如果是当前 addr，可跳过 bind 或使用 Manager 提供的 dry-run。
- TLS 开启时校验证书和私钥。
- 返回证书摘要、过期时间、DNS names 和 warnings。

`POST /api/settings/listener`：

1. 校验 owner session + CSRF。
2. 校验请求体。
3. 如果从 HTTPS 降级 HTTP 或绑定 `0.0.0.0`，要求 `confirm=true` 或确认短语。
4. 先构建并校验 `EndpointConfig`。
5. 先预绑定新 listener。
6. 预加载 TLS cert/key。
7. 持久化 SQLite。
8. 调用 `SwapEndpoint`。
9. 如果 swap 失败，回滚 SQLite 到 previous settings。
10. 写 audit。
11. 返回 new endpoint、redirect URL 和 TLS status。

关键原则：**旧 listener 在新 listener 证明可用前不能关闭**。

## 7. 校验规则

### 7.1 地址校验

- 必须是 `host:port`。
- port 范围 `1-65535`。
- 禁止空 host。
- 允许 `127.0.0.1`、`0.0.0.0`、内网 IP 和公网 IP。
- 绑定 `0.0.0.0` 或公网 IP 时，UI 持续提示公网暴露风险。

### 7.2 证书路径校验

- `TLSEnabled=true` 时 cert/key 必须非空。
- 推荐 cert/key 使用绝对路径。
- 路径 `filepath.Clean` 后保存。
- 不允许 NUL byte。
- 不允许目录路径。
- key 文件不应 world writable。
- 后端只读取文件并校验证书，不返回文件内容。

建议默认引导证书复制到：

```text
<data_dir>/tls/fullchain.pem
<data_dir>/tls/privkey.pem
```

这样服务用户只需要读取自己 data dir 下的文件，避免直接读取 `/etc/letsencrypt/live/...` 的权限复杂度。

### 7.3 证书内容校验

- `tls.LoadX509KeyPair` 必须成功。
- 至少解析 leaf certificate。
- 当前时间必须在 `NotBefore` / `NotAfter` 内。
- cert DNS names 不强制匹配 listen host，因为 listen host 可能是 `0.0.0.0`；UI 可根据用户填写的 public URL 或当前浏览器 host 给 warning。
- 过期小于 14 天显示 warning。

## 8. 安全与审计

### 8.1 Secret 边界

- SQLite 只保存 cert/key 文件路径，不保存私钥正文。
- API 响应不返回 PEM 内容。
- audit 不记录私钥内容。
- 服务日志只记录路径摘要、是否启用、错误摘要、证书 subject 和过期时间。

### 8.2 Session 与 Cookie

`CookieSecure` 继续作为 runtime setting。

推荐 UI 行为：

- TLS enabled 开启时，提示同时开启 Secure Cookie。
- 从 HTTP 切换到 HTTPS 后，若开启 Secure Cookie，当前 session 可能需要重新登录。
- 从 HTTPS 降级 HTTP 时，必须二次确认，并提示 Secure Cookie 会导致 HTTP 页面无法继续携带 session。

### 8.3 降级保护

从 `HTTPS -> HTTP` 是高风险操作：

- risk level: `high`
- UI 二次确认。
- audit event: `settings.listener.updated`
- payload 只记录：
  - `old_scheme`
  - `new_scheme`
  - `old_addr`
  - `new_addr`
  - `tls_enabled`
  - `cert_file_label`

## 9. 前端设计

在 `SettingsView` 的 `服务配置` panel 中扩展：

- 当前有效地址：`https://host:8443` 或 `http://host:port`
- Listen address input
- TLS enabled toggle
- Cert file input
- Key file input
- Test button
- Apply button
- Certificate summary

交互规则：

- TLS disabled 时隐藏或折叠 cert/key 输入，但保留已保存路径摘要。
- Apply 前可以先 Test；Apply 内部仍必须重复校验。
- 保存成功后，如果 scheme/port 变化，前端延迟跳转到新 URL。
- 如果新地址是 `0.0.0.0`，跳转时使用当前浏览器 hostname + 新端口。
- 如果启用 HTTPS，跳转 scheme 改为 `https`。

## 10. Cloudflare 与端口建议

Cloudflare 代理模式只支持固定 HTTPS 端口集合。生产部署如果使用 Cloudflare 橙云，建议使用：

```text
443, 2053, 2083, 2087, 2096, 8443
```

当 `443` 已被其他服务占用时，推荐：

```text
Phantom Lancer HTTPS: 0.0.0.0:8443
Public URL: https://console.example.com:8443
Cloudflare SSL/TLS mode: Full (strict)
```

如果 Docker Registry 需要推送大 layer，Cloudflare 上传限制可能成为瓶颈。此时可切换为 DNS only，让 Docker client 直连 Phantom Lancer 的 HTTPS listener。

## 11. 与 Docker Registry 的关系

Registry 不单独管理 cert/key。它跟随 Phantom Lancer 主 listener：

```text
Web/API/SSE: https://console.example.com:8443/
Registry:   https://console.example.com:8443/v2/*
```

Docker Registry settings 中：

- `public_url` 必须指向主 HTTPS endpoint。
- `require_tls=true` 时 `public_url` 必须是 `https://`。
- push instruction 必须从 `public_url` 派生 Docker host，不把 `https://` 拼入 image reference。

## 12. 测试计划

### 12.1 单元测试

- `RuntimeSettings` 读写 TLS fields。
- listener config normalize。
- cert/key 校验成功。
- cert/key 不匹配失败。
- 过期证书 warning。
- `SwapEndpoint` 新端口 bind 失败时旧 listener 保持可用。
- `SwapEndpoint` TLS cert 失败时旧 listener 保持可用。
- `CertificateReloader` 文件变化后加载新证书。
- `CertificateReloader` 新证书损坏时继续返回旧证书。

### 12.2 集成测试

- HTTP 启动，无 TLS fields。
- API 配置 HTTPS listener，`curl -k https://127.0.0.1:<port>/api/health` 成功。
- HTTPS listener 下 `/v2/` 返回 `401` 或 Registry 预期响应。
- 从 HTTPS 换到另一个 HTTPS 端口。
- 从 HTTPS 降级 HTTP 需要确认。
- Secure Cookie 在 HTTPS 模式下设置 `Secure`。

### 12.3 手工验证

```bash
curl -vk https://console.example.com:8443/api/health
curl -vk https://console.example.com:8443/v2/
openssl s_client -connect console.example.com:8443 -servername console.example.com
```

## 13. 分阶段落地

### P0：闭环 TLS 基础

- RuntimeSettings 增加 TLS fields。
- httpserver.Manager 支持 `EndpointConfig`。
- 启动时按 SQLite settings 选择 HTTP/HTTPS。
- 新增 listener test/apply API。
- Settings 页面支持 TLS enabled、cert path、key path。
- 保存成功热切 listener。

### P1：证书自动 reload

- 增加 `CertificateReloader`。
- TLS listener 使用 `GetCertificate`。
- 增加 TLS status API。
- 页面展示证书过期时间和 reload 状态。

### P2：体验与恢复增强

- HTTPS 降级确认短语。
- 证书快过期提醒。
- bootstrap rescue env：允许临时忽略损坏 TLS 配置并启动 HTTP。
- Registry Public URL 与当前 HTTPS endpoint 联动提示。

## 14. 开放问题

- 是否允许页面填写 `/etc/letsencrypt/live/...`，还是只允许 `<data_dir>/tls/...`？
- HTTPS 切换成功后是否自动开启 Secure Cookie，还是只给推荐提示？
- 启动时 TLS 配置损坏是否默认 fail fast，还是默认降级 HTTP？
- 是否需要支持独立 HTTP redirect listener？第一版建议不做，避免多 listener 生命周期复杂化。

