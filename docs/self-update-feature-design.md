# Phantom Lancer 自更新功能方案

文档日期：2026-06-06  
关联发布地址：<https://github.com/NageNalock/phantom-lancer/releases>  
适用范围：个人服务器 Web 控制台的手动检查更新、下载 release 包、校验、安装和重启。

## 1. 背景与目标

Phantom Lancer 当前采用 Go 单进程后端、SQLite、本地数据目录、SSE 和嵌入式前端静态资源。GitHub Actions 已经在 `v*` tag 上发布 Linux amd64 release 包：

- `phantom-lancer-linux-amd64.tar.gz`
- `phantom-lancer-linux-amd64.tar.gz.sha256`

本功能目标是在 Web 控制台中提供受控的手动更新能力：

- 显示当前版本号。
- 手动检查 GitHub Releases 是否有新版本。
- 如果有新版本，展示即将更新的版本号、发布时间、release 链接和匹配的安装包。
- 用户手动确认后，后端下载 release 包，展示下载进度。
- 下载完成后校验 checksum、解压、安装新 binary，并触发服务重启。
- 更新过程中允许服务短暂不可用，允许当前进程退出，允许已发起的异步任务中断。
- 必须保护用户数据、SQLite 数据库、配置文件、图片资产、日志和运行期设置不被覆盖或删除。

## 2. 非目标

- 不做静默自动更新。
- 不在后台定时自动安装更新。
- 不从前端直接下载 release 包或执行系统命令。
- 不支持任意 URL 更新源。MVP 固定从公开 GitHub Releases 读取。
- 不要求首次版本同时支持所有 OS/arch。当前 release 只支持 `linux/amd64`。
- 不把 GitHub token、代理地址、私有 registry 或私有镜像写进配置、文档、CI 或示例。

## 3. 产品位置

自更新是全局系统能力，放在 `设置` 页面下的 `系统更新` 区块，不提升为一级导航。

控制台首页可以展示轻量提示：

- 当前版本。
- 是否发现可更新版本。
- 点击后跳转到 `设置 -> 系统更新`。

更新确认和进度不放在首页，不打断 Codex、Images、V2Ray 等能力域的正常任务流。

## 4. 版本来源

新增 `internal/buildinfo` 包：

```go
package buildinfo

var (
	Version = "dev"
	Commit  = ""
	Date    = ""
)
```

构建时通过 `-ldflags` 写入：

- tag 构建：`Version=v0.1.0`
- main 构建：`Version=0.0.0-dev+<short-sha>`
- 本地未注入：`dev`

新增 CLI 行为：

```bash
phantom-lancer --version
```

输出简短可解析文本或 JSON。更新安装前必须执行 staged binary 的 `--version`，确认目标版本与 release tag 一致。

## 5. Release 检查策略

后端新增 GitHub release client，只访问公开地址：

- API：`https://api.github.com/repos/NageNalock/phantom-lancer/releases/latest`
- 页面：`https://github.com/NageNalock/phantom-lancer/releases`

默认规则：

- 只接受非 draft、非 prerelease release。
- release tag 必须是 `v` 前缀 semver，例如 `v0.1.0`。
- 当前平台只匹配 `phantom-lancer-linux-amd64.tar.gz`。
- 必须同时存在 `.sha256` asset，否则判定该 release 不可安装。
- 使用 `ETag` / `If-None-Match` 缓存最近一次检查结果，减少 GitHub API rate limit 风险。
- 网络请求设置 User-Agent、超时和响应大小上限。

版本比较：

- 当前版本是正式 semver 且 latest 更大：`updateAvailable=true`。
- 当前版本等于 latest：已是最新。
- 当前版本是 `dev`：展示 latest，但标注“当前为开发构建，无法可靠比较版本”，允许 owner 手动安装 release。
- 平台不匹配或 asset 缺失：展示 latest，但按钮不可用，并说明原因。

## 6. 数据模型

新增表：

```sql
CREATE TABLE IF NOT EXISTS system_update_checks (
  id TEXT PRIMARY KEY,
  current_version TEXT NOT NULL,
  latest_version TEXT NOT NULL DEFAULT '',
  update_available INTEGER NOT NULL DEFAULT 0,
  release_id TEXT NOT NULL DEFAULT '',
  release_url TEXT NOT NULL DEFAULT '',
  asset_name TEXT NOT NULL DEFAULT '',
  asset_url TEXT NOT NULL DEFAULT '',
  checksum_asset_url TEXT NOT NULL DEFAULT '',
  etag TEXT NOT NULL DEFAULT '',
  error_message TEXT NOT NULL DEFAULT '',
  checked_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS system_update_jobs (
  id TEXT PRIMARY KEY,
  current_version TEXT NOT NULL,
  target_version TEXT NOT NULL,
  release_id TEXT NOT NULL,
  asset_name TEXT NOT NULL,
  status TEXT NOT NULL,
  phase TEXT NOT NULL,
  bytes_downloaded INTEGER NOT NULL DEFAULT 0,
  total_bytes INTEGER NOT NULL DEFAULT 0,
  checksum_sha256 TEXT NOT NULL DEFAULT '',
  install_binary_path TEXT NOT NULL DEFAULT '',
  backup_binary_path TEXT NOT NULL DEFAULT '',
  error_message TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  started_at TEXT NOT NULL DEFAULT '',
  completed_at TEXT NOT NULL DEFAULT ''
);
```

事件使用现有 `events` 表：

- `scope = "system_update"`
- `scope_id = <job_id>`

关键事件：

- `update.check.started`
- `update.check.completed`
- `update.job.created`
- `update.download.started`
- `update.download.progress`
- `update.download.completed`
- `update.verify.completed`
- `update.extract.completed`
- `update.install.completed`
- `update.restart.requested`
- `update.failed`
- `update.completed`

## 7. 配置项

配置文件新增可选区块：

```toml
[updates]
enabled = true
repository = "NageNalock/phantom-lancer"
channel = "stable"
asset_name = "phantom-lancer-linux-amd64.tar.gz"
restart_mode = "exit"
install_binary_path = ""
backup_retention = 3
download_timeout_seconds = 300
restart_timeout_seconds = 120
```

说明：

- `repository` MVP 使用固定公开 repo，配置只用于后续迁移，前端不能传任意 repo。
- `restart_mode = "exit"` 是推荐模式：安装完成后后端主动退出，由 systemd `Restart=always` 拉起新进程。
- `install_binary_path` 为空时使用 `os.Executable()` 解析当前 binary。
- 不支持在 Web UI 输入任意 shell restart command 作为 MVP，避免把命令执行面扩大到通用设置。

生产部署建议提供 systemd unit，并设置：

```ini
Restart=always
RestartSec=2
```

如果没有外部 supervisor，更新可以完成 binary 安装，但服务退出后需要用户手动启动。UI 必须在预检阶段明确提示。

## 8. 更新执行链路

### 8.1 预检

点击“下载并更新”后，后端先执行预检：

1. 校验 owner session 和 CSRF。
2. 要求近期敏感操作确认，可复用密码确认或短期 re-auth token。
3. 检查当前 OS/arch 与 asset 匹配。
4. 检查 release tag、asset、checksum asset 是否和最近检查结果一致。
5. 检查当前 binary 路径可解析且目标目录可写。
6. 检查 `data_dir/updates` 可创建，权限使用 `0700`。
7. 检查剩余磁盘空间至少大于包体大小的 3 倍。
8. 检查当前没有其他 update job 运行。
9. 写入 audit：`system.update.start`。

预检失败不下载、不替换任何文件。

### 8.2 下载

下载只由后端完成：

- 下载到 `<data_dir>/updates/staging/<job_id>/archive.tar.gz.part`。
- 使用 streaming copy 统计字节数。
- 每 500ms 或每新增 1MiB 写一次 `update.download.progress` 事件。
- Content-Length 存在时展示百分比，不存在时展示已下载字节。
- 下载完成后 rename 为 `archive.tar.gz`。
- `.sha256` 文件限制最大 4KiB。

下载失败时删除 `.part`，job 标记 failed，不影响现有服务。

### 8.3 校验与解包

必须先校验再解包：

1. 解析 `.sha256`，只接受 64 位 hex SHA-256。
2. 计算 tarball SHA-256，必须完全一致。
3. 使用 Go 标准库 `archive/tar` + `compress/gzip` 解包，不调用外部 `tar`。
4. 防止路径穿越：拒绝绝对路径、`..`、symlink、hardlink 和特殊文件。
5. 只提取 `phantom-lancer/bin/phantom-lancer`，其他文件忽略或保存到 release cache，不覆盖现有配置。
6. 限制解包文件数量和总大小。
7. 给 staged binary 设置 `0755`。
8. 执行 staged binary `--version`，确认版本等于目标 release tag。

任何一步失败都不进入安装阶段。

### 8.4 安装

安装只替换 binary，不覆盖：

- SQLite 数据库。
- `configs/phantom.toml`。
- `.phantom-data` / `/var/lib/phantom-lancer`。
- 图片资产。
- 服务日志。
- 用户工作区。
- Codex/V2Ray/Images 的运行期设置。

安装步骤：

1. 使用 SQLite backup API 或 `VACUUM INTO` 创建更新前 DB 备份：`<data_dir>/backups/pre-update-<job_id>.db`。
2. 复制当前 binary 到 `<data_dir>/updates/backups/phantom-lancer-<current_version>-<timestamp>`。
3. 将 staged binary 复制到目标目录的临时文件：`<install_dir>/.phantom-lancer.<job_id>.tmp`。
4. `fsync` 临时文件。
5. 保持原 binary owner/mode，必要时复制权限。
6. 使用 `rename` 原子替换目标 binary。
7. `fsync` 目标目录。
8. 写入 job 状态 `installed`，写入 `update.install.completed` 事件。

Linux 允许替换正在运行的 binary 文件；当前进程继续运行旧 inode，新进程启动时读取新 binary。

### 8.5 重启与恢复

安装完成后触发重启：

- 推荐模式：后端向 main goroutine 发送 restart request，HTTP handler 返回 `202` 后，服务延迟 300ms 进入有界 graceful shutdown，然后退出。
- 更新重启不能被 SSE、长轮询或其他长连接无限阻塞；graceful shutdown 超过短窗口后必须强制关闭 HTTP server，并以 0 退出，让 systemd 尽快拉起新 binary。此类更新重启超时应作为可恢复 warning 记录，不应把进程退出标记为失败。
- systemd `Restart=always` 拉起新 binary。
- 前端在收到 `update.restart.requested` 后进入 reconnect 状态，轮询 `/api/health`。
- 服务恢复后前端调用 `/api/system/version` 和 `/api/system/update/jobs/<id>`。
- 如果当前版本等于目标版本，显示更新成功。

如果服务在 `restart_timeout_seconds` 内未恢复，前端显示“服务仍在重启或需要手动处理”，并展示最后收到的 job id、目标版本和备份说明。

## 9. 回滚策略

MVP 提供“可恢复”而不是复杂的全自动回滚：

- 每次更新前保留旧 binary 备份。
- 每次更新前保留 SQLite 备份。
- 新版本启动后写入 `system.update.boot_confirmed` audit，标记 job completed。
- 如果新版本能启动但功能异常，UI 后续可提供“回滚到上一版本”按钮。
- 如果新版本无法启动，需要通过服务器 shell 或 supervisor 使用备份 binary 恢复。

后续增强可以加入独立 updater helper，由 helper 负责启动后健康检查和自动回滚。但 MVP 不把 helper 作为硬依赖，避免显著增加部署复杂度。

## 10. API 设计

### `GET /api/system/version`

返回：

```json
{
  "version": "v0.1.0",
  "commit": "abc1234",
  "date": "2026-06-06T00:00:00Z",
  "os": "linux",
  "arch": "amd64"
}
```

### `POST /api/system/update/check`

要求 auth + CSRF。触发一次 GitHub release 检查。

返回：

```json
{
  "currentVersion": "v0.1.0",
  "latestVersion": "v0.1.1",
  "updateAvailable": true,
  "releaseUrl": "https://github.com/NageNalock/phantom-lancer/releases/tag/v0.1.1",
  "assetName": "phantom-lancer-linux-amd64.tar.gz",
  "assetSizeBytes": 18350080,
  "checksumAvailable": true,
  "checkedAt": "2026-06-06T10:00:00Z"
}
```

### `GET /api/system/update/status`

返回最近检查结果、是否有 active job、是否安装后待重启。

### `POST /api/system/update/apply`

要求 auth + CSRF + 敏感操作确认。

请求：

```json
{
  "targetVersion": "v0.1.1",
  "releaseId": "123456",
  "confirmServiceInterruption": true,
  "confirmTaskInterruption": true
}
```

返回 `202 Accepted`：

```json
{
  "job": {
    "id": "upd_xxx",
    "status": "running",
    "targetVersion": "v0.1.1"
  },
  "eventScope": "system_update",
  "eventScopeId": "upd_xxx"
}
```

### `POST /api/system/update/jobs/{id}/cancel`

只允许在 `checking`、`downloading`、`verifying` 阶段取消。进入 `installing` 后不可取消。

## 11. 前端交互

设计语言沿用现有 Quiet Agent Workbench：

- 浅色中性底。
- 小圆角、细边框、低对比状态。
- 版本号、checksum、asset name 使用 monospace。
- warning 使用橙色，success 使用绿色，danger 只用于失败和回滚。
- 不使用营销 hero、插画、渐变背景或装饰动效。

`设置 -> 系统更新` 区块包含：

1. 当前版本行：版本、commit、构建时间、平台。
2. 检查状态行：上次检查时间、最新版本、release 链接。
3. 主操作：
   - `检查更新`
   - `下载并更新`
   - `取消下载`
   - `重试`
4. 更新确认弹窗：
   - 当前版本 -> 目标版本。
   - 安装包名和 checksum 状态。
   - 明确说明服务会短暂不可用，当前异步任务可能中断。
   - 用户必须勾选确认中断后才能开始。
5. 进度区域：
   - 阶段：下载、校验、解包、安装、重启。
   - 下载字节和百分比。
   - 最近事件时间。
6. 错误区域：
   - phase。
   - 简短错误摘要。
   - 可重试动作。

前端状态必须覆盖：

- 未检查。
- 检查中。
- 已是最新。
- 有更新。
- 当前 dev 版本不可比较。
- 平台不支持。
- 下载中。
- 校验中。
- 安装中。
- 重启中。
- 成功。
- 失败。

## 12. 审计与日志

普通服务日志只记录低频边界：

- release 检查失败摘要。
- 下载失败摘要。
- checksum mismatch。
- install/rename 失败。
- restart requested。

不记录：

- 完整下载 URL query。
- 完整 release notes。
- 完整本地个人路径。
- 高频下载进度。

业务 audit 记录 owner 操作：

- `system.update.check`
- `system.update.start`
- `system.update.install`
- `system.update.restart_requested`
- `system.update.completed`
- `system.update.failed`

payload 只放低敏字段：

- currentVersion。
- targetVersion。
- assetName。
- checksum 前 12 位。
- bytesDownloaded。
- status / phase。
- 错误摘要。

下载进度进入 `events`，不写普通服务日志。

## 13. 安全边界

- 所有 API 必须 auth。
- 修改类 API 必须 CSRF。
- `apply` 必须近期 re-auth 或密码确认。
- 前端不能传下载 URL，后端只使用 GitHub API 发现的 release asset。
- 只允许 HTTPS。
- release asset 必须 checksum 校验成功。
- 解包必须防路径穿越和特殊文件。
- 不允许 release 包覆盖配置文件或数据目录。
- 不允许 Web UI 配置任意 shell restart command 作为 MVP。
- 同一时间只允许一个 update job。
- 失败默认保守，不安装、不重启。

## 14. 实现模块

建议新增：

```text
internal/buildinfo/
  buildinfo.go

internal/selfupdate/
  types.go
  github.go
  service.go
  download.go
  archive.go
  installer.go

internal/httpapi/
  selfupdate.go

web/src/features/settings/
  SystemUpdatePanel.tsx
```

`SettingsView` 只负责组合，不把检查、下载、事件渲染等逻辑堆在入口文件里。

## 15. 测试计划

后端单元测试：

- semver 比较。
- `dev` 版本处理。
- GitHub release JSON 解析。
- asset 匹配。
- `.sha256` 解析。
- checksum mismatch 拒绝安装。
- tar path traversal 拒绝。
- symlink/hardlink/special file 拒绝。
- 只提取 binary，不覆盖 configs。
- 原子替换失败时保留旧 binary。
- 同时发起两个 update job 时第二个被拒绝。

HTTP 测试：

- 未登录拒绝。
- 缺 CSRF 拒绝。
- 未确认中断拒绝。
- active job 状态返回。
- events history 能补拉下载进度。

前端测试：

- `npm run check`。
- 手动验证移动端和桌面布局无重叠。
- 下载进度、失败、重启中状态文字不溢出按钮或面板。

集成测试：

- 使用 `httptest.Server` 模拟 GitHub API 和 asset 下载。
- 使用临时目录模拟 binary 安装路径、数据目录和 DB。
- 不在测试中访问真实 GitHub。

## 16. 分阶段落地

### Phase 1：版本与检查更新

- 注入 build version。
- 新增 `/api/system/version`。
- 新增 release check client。
- 设置页显示当前版本和 latest。

### Phase 2：下载、校验、事件进度

- 新增 update job。
- 下载 tarball 和 `.sha256`。
- SSE 展示进度。
- 校验失败不安装。

### Phase 3：安装与重启

- 解包 staged binary。
- 执行 staged `--version`。
- DB 备份。
- 备份旧 binary。
- 原子替换 binary。
- 触发 restart request。
- 前端 reconnect 并确认新版本。

### Phase 4：回滚与增强

- UI 回滚按钮。
- release manifest。
- 多平台 asset。
- 独立 updater helper 和自动健康检查回滚。

## 17. 关键决策

MVP 只自动替换 binary，不自动覆盖 release 包里的 `configs/phantom.example.toml`、`scripts/`、`README.md` 和 `LICENSE` 到安装目录。这样能最大程度保证配置不丢失，并避免脚本变更影响当前 supervisor。

如果未来必须更新脚本，应单独设计“脚本更新”步骤，并在 UI 中展示 diff 和确认，而不是随 binary 更新隐式覆盖。
