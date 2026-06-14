# Grokbox 图片生成模块功能设计

文档日期：2026-06-05  
来源服务：本地 Grokbox 参考实现
关联文档：

- [personal-web-terminal-product-features.md](./personal-web-terminal-product-features.md)
- [personal-web-terminal-technical-design.md](./personal-web-terminal-technical-design.md)
- [images-library-feature-design.md](./images-library-feature-design.md)

## 0. 历史兼容说明

本文是早期 Grokbox/xAI 图片生成迁移设计，描述的是多媒体能力域的图片生成子集。当前用户可见一级导航和产品文案已升级为“多媒体”，覆盖图片生成、视频生成、多图编辑、关键帧、资源库、历史和生成预设。

为了兼容旧路由、旧 SQLite 表、旧事件和旧前端查询，代码和 API 中仍会保留 `images`、`image_*`、`/api/images/*`、`images.*` 等历史命名。后续参考本文时应遵守：

- 用户可见导航和新文档优先使用“多媒体”。
- 本文中的 `Images` 可理解为当前多媒体模块的历史图片生成子域。
- 本文中的 `Prompt` 模板能力已演进为“生成预设”，可保存提示词、模型、模式和常用参数；默认不保存参考图引用。
- 本文中的图片库能力已被 [多媒体资源库与对象存储功能设计](./images-library-feature-design.md) 扩展为图片/视频资源库。
- 若本文与 [Agnes 图片与视频生成接入设计](./agnes-image-video-integration-design.md) 或产品/技术总文档冲突，以后者为准。

## 1. Design Read

Reading this as: 个人服务器控制台里的 AI 图片生成工作台，面向单 owner 技术用户，采用 Quiet Agent Workbench / Quiet DevOps Control Plane 语言，强调受控调用、密钥边界、历史可追踪、低噪音参数面板和结果归档。

本模块不是营销页，也不应该把 Grokbox 原来的独立暗色视觉、品牌化标题、serif 字体和 emoji 按钮搬进 Phantom Lancer。它应像一个受控的模型调用工作台：左侧保持全局导航，主工作区围绕生成任务展开，右侧 inspector 显示 provider、密钥状态、当前参数、调用历史和风险提示。

## 2. 迁移目标

将 Grokbox 服务的功能整合为 Phantom Lancer 的一个内置模块。迁移方式是功能搬迁和架构重构，不做源码拷贝，不保留 Grokbox 自己的独立登录页、内存 session、HTML template、CSS/JS 静态文件和 JSON 文件存储。

保留的核心能力：

- 调用 xAI Grok Imagine 图片模型。
- 支持文生图、图生图、多图编辑三种模式。
- 支持上传参考图或输入参考图 URL。
- 支持模型、比例、分辨率、返回格式和生成数量等参数。
- 记录每一次有效调用，包括成功和失败。
- 成功调用保存生成图片的本地副本。
- 提供历史列表、结果查看、原始远程图片链接和 revised prompt 展示。
- 提供以图片资产为中心的图片库，支持生成图和用户上传参考图的放大查看、下载、删除和元数据 inspector。
- 支持默认本地保存；配置对象存储后，生成结果优先通过 S3 API 兼容 SDK 保存到 S3-compatible 对象存储，例如阿里云 OSS、腾讯云 COS、MinIO、Cloudflare R2 或其他兼容服务。
- 本地图片资产支持手动归档到 S3；S3 bucket 可以保持私有，不要求 public read。
- 管理 xAI API Key，但必须融入当前项目的设置、密钥和审计边界。

替换的基础能力：

- Grokbox 的登录密钥替换为 Phantom Lancer owner 账号、session cookie 和 CSRF。
- Grokbox 的 `data/config.json` 替换为 SQLite 中的模块设置或后续 Secret Store。
- Grokbox 的 `data/history.json` 替换为 SQLite 调用记录。
- Grokbox 的 `data/history-images/` 替换为 Phantom Lancer data dir 下的受控图片资产目录。
- Grokbox 的同步 HTTP 调用体验升级为可恢复的 generation job 和事件流。
- Grokbox 的独立 UI 替换为 React + Vite 里的控制台模块页面。

## 3. Grokbox 现有功能梳理

### 3.1 服务形态

Grokbox 是一个 Go 单 binary Web app，使用标准库 `net/http`，同一进程同时提供 HTML 页面、CSS/JS 静态资源和 API。

运行参数：

- `ADDR`：监听地址，默认 `127.0.0.1:8087`。
- `GROKBOX_CONFIG`：配置文件路径，默认 `data/config.json`。
- `GROKBOX_HISTORY`：历史文件路径，默认 `data/history.json`。
- `GROKBOX_HISTORY_IMAGES`：历史图片目录，默认 `data/history-images`。
- `GROKBOX_LOGIN_KEY`：首次启动时可用于 bootstrap 登录密钥。
- `XAI_API_KEY`：首次启动时可用于 bootstrap xAI API Key。

这些运行参数不应直接进入 Phantom Lancer。迁移后使用 `PL_DATA_DIR`、SQLite settings、模块页面设置和后续 secret 管理。

### 3.2 鉴权与设置

现有 Grokbox 能力：

- 首次访问未配置时进入 `/setup`。
- 设置登录密钥，最短 12 字符。
- 登录密钥使用随机 salt + SHA-256 hash 保存。
- 登录后创建 12 小时内存 session。
- session cookie 为 `HttpOnly`、`SameSite=Strict`，TLS 下启用 `Secure`。
- 每个会修改状态或调用模型的请求都校验 CSRF。
- 设置弹窗支持更新登录密钥、设置 xAI API Key、清除 xAI API Key。
- API Key 在界面只显示 masked 形式。

迁移判断：

- 登录密钥、setup/login/logout 页面全部废弃。
- CSRF 要保留语义，但复用当前 `pl_session` / `pl_csrf` 机制。
- xAI API Key 作为 Images 模块自己的 provider 设置，不放到通用 Settings 的全局运行设置里。
- 保存、更新、清除 API Key 都必须写 audit，audit 只记录 masked 状态或 key hash，不记录明文。

### 3.3 图片调用能力

现有 Grokbox 支持三种模式：

| 模式 | 内部值 | xAI endpoint | 参考图要求 |
| --- | --- | --- | --- |
| 文生图 | `text_to_image` | `/images/generations` | 不允许参考图 |
| 图生图 | `image_to_image` | `/images/edits` | 必须且只能 1 张 |
| 多图编辑 | `multi_image_edit` | `/images/edits` | 必须 2 到 3 张 |

支持参数：

- `prompt`：必填，trim 后不能为空，最大 8000 字符。
- `model`：默认 `grok-imagine-image-quality`，现有 UI 还提供 `grok-imagine-image`。
- `aspect_ratio`：允许空值、`1:1`、`16:9`、`9:16`、`4:3`、`3:4`、`3:2`、`2:3`。
- `resolution`：允许空值、`1k`、`2k`。
- `response_format`：允许 `url`、`b64_json`，默认 `url`。
- `n`：生成数量，默认 1，范围 1 到 10。
- 参考图：每个 slot 可上传文件或输入 URL，最多 3 个 slot。

参考图输入约束：

- 单张上传图片最大 12 MB。
- 总 multipart form 最大 40 MB。
- 上传图片 MIME 允许 `image/jpeg`、`image/png`、`image/gif`、`image/webp`。
- URL 最大 4096 字符。
- URL scheme 允许 `http`、`https`、`data:image/`。
- HTTP/HTTPS URL 必须有 host。
- 上传文件会转成 `data:<mime>;base64,<payload>` 再作为 `image_url` 传给 xAI。

调用约束：

- xAI 请求 timeout 为 145 秒上下文，HTTP client 总 timeout 为 150 秒。
- 上游非 2xx 响应会读取最多 64 MB 响应体并返回错误。
- 错误消息会移除明文 API Key。
- 缺少 API Key 时返回 `xAI API key is not configured`。

### 3.4 历史与图片归档

现有 Grokbox 每次通过校验的调用都会写历史：

- 成功调用记录参数、endpoint、生成图片、usage 和 completed time。
- 失败调用记录参数、endpoint、错误消息和 completed time。
- 历史默认查询 50 条，查询 limit 最大 200。
- 本地最多保留 500 条历史记录。
- 超过 500 条后清理被移除记录对应的本地图片。
- 生成图片如果是 `b64_json`，直接解码落盘；如果是 URL，尝试下载远程图片并落盘。
- 落盘图片单张最大 32 MB。
- 本地图片文件权限为 `0600`，目录权限为 `0700`。
- 如果本地落盘失败，历史仍保留远程 URL。

迁移判断：

- 历史必须进入 SQLite，而不是 JSON 文件。
- 图片文件继续放在 data dir，但路径必须由后端生成和校验。
- 静态图片访问应保持 authenticated，不做公开文件服务。
- 失败记录同样有价值，必须保留，以便审计模型调用尝试和排查上游错误。

### 3.5 UI 现状

现有页面包含：

- setup 页面。
- login 页面。
- Grok 图片模型工作台页面。
- 顶部标题与设置/退出按钮。
- 左侧状态 rail：API Key 状态、模式、模型、历史数量。
- 主表单：模式 segmented control、prompt textarea、模型、比例、分辨率、数量、响应格式、参考图 URL/file slots。
- 结果面板：本次结果、历史结果、刷新、清空。
- 设置 dialog：当前登录密钥、新登录密钥、xAI API Key、清除 API Key。

迁移判断：

- setup/login/logout 不迁移。
- 工作台的信息结构可保留，但视觉语言必须重做为当前控制台风格。
- 设置 dialog 中的登录密钥字段删除，只保留 provider/API Key 和模块默认参数。
- 结果和历史应该作为同一模块下的二级视图或 tabs，不放入全局 Activity。

## 4. 产品边界

### 4.1 MVP 范围

- 新增一个 AI 图片生成模块，建议一级导航命名为 `Images` 或 `Imagine`，页面标题可显示 `Grok Imagine` provider。
- Dashboard 展示模块摘要：API Key 是否配置、最近一次调用状态、今日调用数、最近失败。
- 模块页面支持文生图、图生图、多图编辑。
- 支持 URL 和本地上传参考图。
- 支持 Grokbox 现有参数范围。
- 支持保存 xAI API Key、清除 API Key、显示 masked API Key。
- 支持创建 generation job、查看当前结果、查看历史。
- 成功和失败调用都写 SQLite 历史。
- 成功结果尽力保存本地图片副本。
- 提供图片库视图，管理之前每次成功生成的图片和用户上传参考图。
- 支持图片放大查看、下载、删除和右侧元数据 inspector。
- 支持 S3 API 兼容对象存储作为 Images 图片持久化后端；未配置时默认本地保存。这里的 S3 表示协议兼容，不要求真实 AWS S3。
- 支持将已存在本地的图片资产归档到 S3。
- 支持 authenticated 图片资产访问。
- 调用、设置变更、失败、图片保存失败等关键事件进入 audit。

### 4.2 非目标

- 不迁移 Grokbox 独立登录和首次配置流程。
- 不保留 Grokbox 独立端口或 sidecar 服务。
- 不拷贝 Grokbox 的 HTML template、CSS、vanilla JS。
- 不在 MVP 做多 provider 抽象到过度复杂；但数据模型应保留 provider 字段。
- 不做团队共享图库、多用户图库权限、公开分享页面。
- 不做社交相册、公开作品集、评论协作或复杂 DAM 系统。
- 不自动购买、检测或管理 xAI 账号额度。
- 不在 audit 中记录 API Key 明文或完整图片 base64。
- 不把模块设置塞进通用 Settings 的运行设置区。

## 5. 信息架构

Grokbox 是独立完整任务流，不属于 Codex、V2Ray 或通用 Settings。建议作为独立一级能力进入主导航：

- `控制台`：展示 Images 摘要，不展开完整表单。
- `Codex`：保持工作区、会话、权限、活动。
- `Images` / `Imagine`：图片生成、编辑、历史和 provider 设置。
- `V2Ray`：网络服务控制面。
- `设置`：只保留全局设置，例如允许根目录、Cookie、安全策略。

Images 模块内部建议二级结构：

- `Generate`：当前生成任务，包含 prompt、参数、参考图和本次结果。
- `Library`：以图片资产为中心管理生成结果和用户上传参考图，支持查看、下载、删除、归档到 S3 和元数据 inspector。
- `History`：历史调用记录，支持查看成功/失败、参数摘要、图片资产。
- `Settings`：xAI provider 状态、API Key、默认模型、默认 response format、历史保留策略和图片存储设置。

页面布局：

- 顶部状态条：provider、API Key 状态、当前 job 状态、最近错误。
- 主工作区：生成表单和结果。
- 右侧 inspector：当前参数摘要、参考图数量要求、usage、历史数量、最近失败、保留策略。
- History 视图：按时间倒序列表，支持成功/失败筛选和 limit。
- Library 视图：按图片资产倒序展示，右侧 inspector 展示选中图片的 asset type、job、slot、prompt、model、MIME、尺寸、大小、checksum、storage backend、local name 或 S3 object key 等元数据。

图片库的详细功能、交互方式和对象存储设计见 [images-library-feature-design.md](./images-library-feature-design.md)。

## 6. 用户流程

### 6.1 首次启用

1. Owner 打开 `Images`。
2. 页面显示 xAI API Key 未配置，生成按钮 disabled。
3. Owner 打开模块 `Settings`，输入 xAI API Key。
4. 后端保存 secret，返回 masked API Key。
5. 系统写入 `images.settings.updated` audit。
6. 页面回到 `Generate`，显示 provider 可用。

### 6.2 文生图

1. Owner 选择 `文生图`。
2. 输入 prompt，选择模型、比例、分辨率、数量和响应格式。
3. 点击生成。
4. 后端校验参数并创建 generation job。
5. API 立即返回 job id 和初始状态，不等待 xAI 调用完成。
6. 后端后台 worker 调用 xAI `/images/generations`。
7. 页面通过轮询或事件流看到 job 状态变化。
8. 成功后展示图片、本地副本链接、revised prompt 和 usage。
9. 调用记录进入 History 和 audit。

### 6.3 图生图

1. Owner 选择 `图生图`。
2. 页面只展示 1 个参考图 slot。
3. Owner 上传文件或填写 URL。
4. 后端校验 exactly one source image。
5. 后端调用 xAI `/images/edits`，payload 字段使用 `image`。
6. 结果进入当前视图和历史。

### 6.4 多图编辑

1. Owner 选择 `多图编辑`。
2. 页面展示 3 个参考图 slot，并提示需要 2 到 3 张。
3. 后端校验 source image 数量。
4. 后端调用 xAI `/images/edits`，payload 字段使用 `images`。
5. 结果进入当前视图和历史。

### 6.5 失败恢复

1. 上游返回非 2xx、超时或本地下载图片失败。
2. 后端记录失败 job 和错误摘要。
3. API Key 明文从错误中 redacted。
4. 页面展示错误，但历史保留这次尝试。
5. audit 记录风险等级和失败摘要。

### 6.6 刷新和断线恢复

Images 生成必须是真正异步的后台任务，不能绑定到创建请求的 HTTP context。

1. Owner 点击生成后，后端完成参数校验、保存 job，再立即返回 `202 Accepted` 和 job id。
2. xAI 调用由后端后台 worker 继续执行，浏览器刷新、关闭标签页或网络断开都不应取消该 job。
3. 页面重新打开后，通过 `GET /api/images/jobs` 或 `GET /api/images/jobs/{id}` 恢复查看 `queued`、`running`、`success`、`failed` 等状态。
4. 如果后端进程重启，未完成 job 必须被恢复执行或保守标记为 `failed` / `interrupted`，不能永久停留在 `running`。
5. 同一个创建请求后续可引入 client request id，用于避免用户刷新或重试导致重复提交。

## 7. 后端模块设计

建议新增模块 `internal/images`。

职责：

- `Service`：协调设置、job 创建、xAI 调用、历史写入、事件发布和审计。
- `Provider` / `XAIClient`：封装 xAI image generation/edit API。
- `Validator`：校验 mode、prompt、model、aspect ratio、resolution、response format、count 和 source image。
- `AssetStore`：保存和读取本地图片资产，负责文件名、MIME、大小、权限和路径安全。
- `HistoryRepository`：读写 SQLite 调用记录。
- `SettingsRepository`：读写 provider 设置和 masked 状态。

建议目录：

```text
internal/images/
  service.go
  xai.go
  validation.go
  assets.go
  types.go
```

HTTP 层仍放在 `internal/httpapi`，只负责 auth/CSRF、request decode、response encode 和调用 `images.Service`。创建 generation job 时，HTTP 层不得同步等待 xAI 返回；它只负责创建 job、触发后台执行并返回 job id。

## 8. API 草案

所有接口都需要 owner session。写操作必须校验 CSRF。

| Method | Path | 用途 |
| --- | --- | --- |
| `GET` | `/api/images/status` | 获取 provider、API Key、最近 job、历史数量摘要 |
| `GET` | `/api/images/settings` | 获取模块设置和 masked API Key |
| `PUT` | `/api/images/settings` | 更新 API Key、默认模型、默认参数和历史保留策略 |
| `POST` | `/api/images/jobs` | 创建图片生成 job，使用 multipart form；成功时返回 `202 Accepted` 和 job id，后台异步执行 |
| `GET` | `/api/images/jobs` | 查询历史 job 列表，支持 `limit`、`status`、`mode` |
| `GET` | `/api/images/jobs/{id}` | 查询单个 job 详情 |
| `GET` | `/api/images/assets/{name}` | 读取本地图片资产，需要鉴权 |
| `GET` | `/api/images/library/assets` | 查询图片库资产，支持分页、搜索、storage 和 mode 筛选 |
| `GET` | `/api/images/library/assets/{id}` | 查询单张图片资产详情 |
| `GET` | `/api/images/library/assets/{id}/download` | 下载图片资产，需要鉴权 |
| `DELETE` | `/api/images/library/assets/{id}` | 删除图片资产，需要 CSRF |
| `POST` | `/api/images/library/assets/{id}/archive-s3` | 将本地图片资产归档到 S3，需要 CSRF |
| `GET` | `/api/images/storage-settings` | 获取 Images 图片存储设置，secret 只返回 masked 状态 |
| `PUT` | `/api/images/storage-settings` | 更新 local / S3 存储设置，需要 CSRF |
| `POST` | `/api/images/storage-settings/test` | 测试 S3 连接和写权限，需要 CSRF |

`POST /api/images/jobs` 请求字段沿用 Grokbox 语义：

- `mode`
- `prompt`
- `model`
- `aspect_ratio`
- `resolution`
- `response_format`
- `n`
- `image_url_1` / `image_file_1`
- `image_url_2` / `image_file_2`
- `image_url_3` / `image_file_3`

事件建议：

- `images.job.created`
- `images.job.queued`
- `images.job.started`
- `images.job.completed`
- `images.job.failed`
- `images.job.interrupted`
- `images.asset.stored`
- `images.asset.store_failed`
- `images.asset.deleted`
- `images.asset.delete_failed`
- `images.asset.stored.s3`
- `images.asset.s3_upload_failed`
- `images.asset.archived.s3`
- `images.asset.archive_failed`
- `images.settings.updated`
- `images.storage.settings.updated`

事件 scope 可使用 `images` + job id。历史补拉复用当前 `/api/events/history` 和 `/api/events/stream` 机制。

## 9. SQLite 数据模型草案

### 9.1 模块设置

```sql
CREATE TABLE IF NOT EXISTS image_provider_settings (
  id TEXT PRIMARY KEY,
  provider TEXT NOT NULL DEFAULT 'xai',
  api_key_ciphertext TEXT NOT NULL DEFAULT '',
  api_key_fingerprint TEXT NOT NULL DEFAULT '',
  default_model TEXT NOT NULL DEFAULT 'grok-imagine-image-quality',
  default_response_format TEXT NOT NULL DEFAULT 'url',
  default_resolution TEXT NOT NULL DEFAULT '',
  default_aspect_ratio TEXT NOT NULL DEFAULT '',
  history_retention INTEGER NOT NULL DEFAULT 500,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
```

如果 Secret Store 尚未落地，第一版也可以先把 provider setting 存为 SQLite settings key，但服务接口仍应隐藏实现细节，便于后续迁移到专门 secret 表。

### 9.2 生成 job

```sql
CREATE TABLE IF NOT EXISTS image_generation_jobs (
  id TEXT PRIMARY KEY,
  provider TEXT NOT NULL DEFAULT 'xai',
  status TEXT NOT NULL,
  mode TEXT NOT NULL,
  mode_label TEXT NOT NULL,
  model TEXT NOT NULL,
  endpoint TEXT NOT NULL DEFAULT '',
  prompt TEXT NOT NULL,
  aspect_ratio TEXT NOT NULL DEFAULT '',
  resolution TEXT NOT NULL DEFAULT '',
  response_format TEXT NOT NULL DEFAULT 'url',
  image_count INTEGER NOT NULL DEFAULT 1,
  source_count INTEGER NOT NULL DEFAULT 0,
  usage_json TEXT NOT NULL DEFAULT '{}',
  error_message TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  started_at TEXT NOT NULL DEFAULT '',
  completed_at TEXT NOT NULL DEFAULT ''
);
```

`status` 取值要求：

- `queued`：job 已入库，等待后台 worker 执行。
- `running`：后台 worker 正在调用 provider 或保存图片资产。
- `success`：provider 调用成功，输出记录已写入。
- `failed`：参数外的运行失败、provider 失败或本地保存后的最终失败。
- `interrupted`：服务重启、显式取消或后台执行被保守中断。

创建 job 时应先写入 `queued`，后台 worker 认领后改为 `running`。HTTP 请求取消不得直接取消 `queued` / `running` job。

### 9.3 输入源和输出图片

```sql
CREATE TABLE IF NOT EXISTS image_generation_sources (
  id TEXT PRIMARY KEY,
  job_id TEXT NOT NULL,
  slot INTEGER NOT NULL,
  source_type TEXT NOT NULL,
  source_label TEXT NOT NULL DEFAULT '',
  mime_type TEXT NOT NULL DEFAULT '',
  size_bytes INTEGER NOT NULL DEFAULT 0,
  url_redacted TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS image_generation_outputs (
  id TEXT PRIMARY KEY,
  job_id TEXT NOT NULL,
  slot INTEGER NOT NULL,
  remote_url TEXT NOT NULL DEFAULT '',
  local_name TEXT NOT NULL DEFAULT '',
  mime_type TEXT NOT NULL DEFAULT '',
  revised_prompt TEXT NOT NULL DEFAULT '',
  storage TEXT NOT NULL DEFAULT '',
  size_bytes INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL
);
```

图片库应引入统一 `image_assets` 表承载生成输出图和用户上传参考图，并让 `image_generation_outputs.asset_id`、`image_generation_sources.asset_id` 指向它。资产字段至少包含 `asset_type`、`status`、`storage_backend`、`local_name`、`s3_key`、`checksum_sha256`、`width`、`height`、`archived_at`、`deleted_at` 和 `last_error`。

索引：

- `idx_image_jobs_created_at` on `image_generation_jobs(created_at DESC)`
- `idx_image_jobs_status` on `image_generation_jobs(status, created_at DESC)`
- `idx_image_outputs_job` on `image_generation_outputs(job_id, slot)`

## 10. 参数校验规则

迁移后应保留 Grokbox 当前校验，并将错误码结构化：

- `prompt_required`
- `prompt_too_long`
- `model_invalid`
- `mode_invalid`
- `source_count_invalid`
- `aspect_ratio_unsupported`
- `resolution_unsupported`
- `response_format_unsupported`
- `image_count_invalid`
- `image_too_large`
- `image_mime_unsupported`
- `image_url_invalid`
- `api_key_missing`
- `provider_failed`

后端错误 response 继续使用当前项目格式：

```json
{
  "error": {
    "code": "prompt_required",
    "message": "Prompt 不能为空"
  }
}
```

## 11. 文件与资产边界

建议图片资产目录：

```text
<PL_DATA_DIR>/images/generated/
```

规则：

- 目录权限 `0700`。
- 文件权限 `0600`。
- 文件名由后端生成，使用 job id、slot 和 MIME 扩展名。
- `GET /api/images/assets/{name}` 必须校验 authenticated session。
- 文件名只允许 `[A-Za-z0-9._-]+`。
- 读取文件时必须确认最终路径仍在 images asset dir 内。
- 不在 API response 中返回服务器绝对路径。
- 远程 URL 和本地 URL 分开保存；本地副本优先展示。

对象存储边界：

- 默认 `local`，继续保存到 `<PL_DATA_DIR>/images/generated/`。
- 配置 `s3` 后，生成结果优先通过 S3 API 兼容 SDK 上传对象存储。
- `s3` 不等于 AWS S3；非 AWS provider 必须配置 endpoint，region 只作为 provider 需要的 SDK 参数透传。
- object key 由后端生成，不包含 prompt、上传文件名或用户输入。
- S3 secret、session token、signed URL query 不进入前端 response、audit 和日志明文。
- S3 上传成功后不保留完整本地原图，除非用户显式启用 fallback/cache。
- S3 上传失败默认 fallback local，并写入 `images.asset.s3_upload_failed`。
- S3 bucket 可以保持私有；S3 图片读取默认通过后端代理，短 TTL presigned URL 只作为可选优化，前端不接触 access key。
- 本地图片资产可以手动归档到 S3，归档成功后删除本地完整原图。

## 12. 权限、审计与安全

权限能力建议：

- `images.read`：查看模块状态、历史和图片资产。
- `images.run`：发起模型调用。
- `images.settings.write`：更新 provider 设置和 API Key。
- `images.asset.read`：读取生成图片资产。
- `images.history.delete`：后续如支持删除历史，需要单独能力。
- `images.library.read`：查看图片库和图片元数据。
- `images.asset.download`：下载图片资产。
- `images.asset.delete`：删除图片资产。
- `images.asset.archive`：将本地图片资产归档到 S3。
- `images.storage.settings.write`：更新对象存储设置。

审计事件建议：

- `images.settings.updated`：API Key 设置、清除、默认参数变化。
- `images.job.created`：创建调用，记录 mode、model、source_count、n，不记录完整图片 base64。
- `images.job.completed`：调用成功，记录 usage、output count、是否保存本地副本。
- `images.job.failed`：调用失败，记录 redacted 错误。
- `images.asset.store_failed`：上游成功但本地保存失败。
- `images.asset.deleted`：图片资产删除成功，记录 asset id、job id、storage backend，不记录本地绝对路径或完整 S3 secret。
- `images.asset.delete_failed`：图片资产删除失败，记录 redacted 错误。
- `images.asset.archived.s3`：本地图片归档到 S3 成功，记录 asset id、job id、object key hash 和大小。
- `images.asset.archive_failed`：归档到 S3 失败，记录 redacted 错误。
- `images.storage.settings.updated`：对象存储设置更新，记录 backend、bucket/endpoint 摘要和是否更新 secret，不记录 secret 明文。

安全要求：

- xAI API Key 不进入前端明文、不进入 audit 明文、不进入错误明文。
- 上传文件不落临时公开目录。
- 限制 multipart body、单图大小、输出图片下载大小和上游响应读取大小。
- 远程图片下载设置 timeout，不跟随危险路径写入。
- 对 `data:image/` 输入设置大小上限，避免超大 base64 占用内存。
- 生成任务必须有 owner session 和 CSRF。
- 删除图片必须有 owner session 和 CSRF。
- 归档图片到 S3 必须有 owner session 和 CSRF。
- 图片下载必须走后端受控接口或短 TTL presigned URL，不能暴露服务器绝对路径或 S3 凭证。

## 13. 前端设计要求

页面必须沿用当前 Phantom Lancer 的控制台语言：

- 浅色中性底、细边框、小圆角、克制强调色。
- 使用当前组件体系，例如 `Panel`、`Button`、`ContextList` 等。
- 不使用 Grokbox 原来的暗色暖调背景、serif 标题、酸绿色主色、emoji 图标。
- 模式选择用低对比 segmented control。
- 上传图 slot 使用稳定尺寸，避免选择文件后布局跳动。
- 生成按钮需要 loading、disabled、error 和 success 状态。
- History 列表可以更密集，但失败态、参数摘要、usage 和本地/远程图片链接要清晰。
- xAI API Key 设置放在 Images 模块内部的 Settings tab、drawer 或 inspector，不放在全局 Settings。

建议页面结构：

- `web/src/features/ImagesView.tsx`
- `web/src/images/types.ts`
- `web/src/images/validation.ts`
- `web/src/images/components/GeneratePanel.tsx`
- `web/src/images/components/LibraryPanel.tsx`
- `web/src/images/components/ImageAssetViewer.tsx`
- `web/src/images/components/ImageAssetInspector.tsx`
- `web/src/images/components/HistoryPanel.tsx`
- `web/src/images/components/ProviderSettings.tsx`
- `web/src/images/components/ImageStorageSettings.tsx`

入口组件只负责路由、数据装配和模块组合，不应堆叠所有表单、历史卡片和设置弹窗实现。

## 14. 分阶段落地

### Phase 1：功能等价迁移

- 后端新增 `internal/images`。
- 新增 SQLite migration。
- 新增 `/api/images/*`。
- `/api/images/jobs` 创建后立即返回 job id，后台异步执行 xAI 调用。
- 前端新增 `Images` 一级导航和模块页面。
- 支持 Grokbox 现有三种调用模式和参数。
- 支持历史、图片资产、本地副本和失败记录。
- 支持 Library 图片库查看生成图和上传参考图、放大、下载、删除和右侧 metadata inspector。
- 写入审计。

### Phase 2：控制台化增强

- generation job 事件接入 SSE。
- Dashboard 加 Images 摘要。
- History 增加筛选、重试、复制参数。
- Library 增加搜索、storage/mode 筛选、多选和批量删除。
- Settings 支持默认参数和历史保留上限。
- Settings 支持 S3 兼容对象存储配置、连接测试和上传失败 fallback 策略。
- Library 支持本地图片归档到 S3。
- 图片保存失败提供单条重试。

### Phase 3：能力扩展

- Secret Store 抽象。
- 多 provider 预留，例如 OpenAI Images 或本地模型。
- 图片收藏、标签、批量下载。
- 本地图片迁移到 S3。
- 本地缩略图缓存和清理策略。
- Prompt 模板和最近参数 preset。
- 高成本调用确认和审计摘要。

## 15. 验收标准

- Owner 登录后可以在 Phantom Lancer 内完成文生图、图生图和多图编辑。
- 未配置 xAI API Key 时不能发起调用，页面给出清晰状态。
- 成功调用能展示图片并在刷新后从 History 恢复。
- 生成中刷新页面后，后台 job 继续执行；重新进入 Images 后能通过 History 或 job 详情看到最终结果或失败原因。
- 成功生成的图片和用户上传参考图能在 Library 中按图片资产查看，并支持放大、下载和删除。
- Library 右侧 inspector 能展示图片的 asset type、job、slot、prompt、model、storage backend、MIME、尺寸、大小、checksum 和创建时间。
- 未配置对象存储时默认保存到本地；配置 S3 后优先保存到对象存储。
- 已存在本地的图片资产能归档到 S3；归档失败不能破坏本地文件。
- S3 bucket 不需要公开读，默认通过后端代理读取。
- S3 上传失败时有 fallback local 或明确失败状态，不能无声丢失图片。
- 失败调用也能在 History 中看到参数摘要和 redacted 错误。
- 本地图片资产只能在登录后访问。
- API Key 不出现在前端 response、audit、日志和错误消息中。
- `go test ./...` 覆盖参数校验、payload 生成、历史写入和资产路径校验。
- 前端通过 `npm run build`，移动和桌面布局不发生控件重叠。

## 16. 当前源服务验证

已在 Grokbox 源目录使用隔离 Go cache 执行：

```bash
env GOCACHE=/private/tmp/grokbox-go-cache GOMODCACHE=/private/tmp/grokbox-go-mod-cache go test ./...
```

结果：通过。
