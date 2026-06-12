# Agnes 图片与视频生成接入设计

文档日期：2026-06-13

关联文档：

- [grokbox-module-feature-design.md](./grokbox-module-feature-design.md)
- [images-library-feature-design.md](./images-library-feature-design.md)
- [personal-web-terminal-product-features.md](./personal-web-terminal-product-features.md)
- [personal-web-terminal-technical-design.md](./personal-web-terminal-technical-design.md)

Agnes 公开文档来源：

- [Agnes Docs Index](https://agnes-ai.com/api/docs?lang=en)
- [Agnes Image 1.2 Deprecated](https://agnes-ai.com/doc/agnes-image-12)
- [Agnes Image 2.0 Flash](https://agnes-ai.com/doc/agnes-image-20-flash)
- [Agnes Image 2.1 Flash](https://agnes-ai.com/doc/agnes-image-21-flash)
- [Agnes Video V1.2 Deprecated](https://agnes-ai.com/doc/agnes-video-v12)
- [Agnes Video V2.0](https://agnes-ai.com/doc/agnes-video-v20)

## 1. Design Read

Reading this as: 个人服务器控制台里的 AI media generation 工作台扩展，面向单 owner 技术用户，必须延续现有 Images/Grok 的低噪音 job 流程、受控资产库、provider 设置和审计边界；视觉与交互采用 Quiet Agent Workbench / Quiet DevOps Control Plane 语言，而不是把 Agnes 文档站的品牌视觉、营销文案或模型 showcase 搬进控制台。

本次接入不是重做 Images 模块，也不是新增一个脱离当前资产库的 Video App。正确方向是：保持现有 Grok 操作流程基本不变，把 Agnes 作为增量 provider 接入图片生成，并在同一个能力域下补齐视频生成、视频资产、视频历史和视频 provider 状态。UI 上应继续围绕 `Generate / Library / History / Settings` 展开，新增的图片/视频差异通过低噪音 segmented control、参数分组和 inspector 解释。

设计 pre-flight：

- 保持 `Images` 一级导航，避免破坏旧入口、旧路由和用户肌肉记忆；视频能力作为 Images 能力域下的 media scope 扩展进入。
- 不做营销 hero、大插画、渐变背景、品牌色大面积铺陈或“模型广场”式卡片。
- 主流程仍是：输入 prompt 和参数，选择参考图，创建异步 job，观察状态，保存结果到 Library，History 可追溯。
- 所有新增状态、错误、资产、轮询和 provider 设置都必须可审计、可恢复、可清理。
- Text 类 Agnes 模型本阶段明确不接入，不进入 Images provider catalog，也不暴露在 UI 模型选择里。

## 2. 背景与目标

当前 Images 模块以 xAI Grok Imagine 为 provider，已经支持：

- 文生图、图生图、多图编辑。
- URL、本地上传、Library asset 作为参考图。
- 异步 generation job。
- History 调用记录。
- Library 图片资产管理。
- 本地和 S3-compatible 对象存储。
- provider 设置、API Key masked 状态、audit 和事件记录。

Agnes 文档显示它不止有图片模型，还提供视频生成模型。接入时需要避免两种风险：

- 只把 Agnes Image 2.1 Flash 硬塞进当前 Grok 表单，导致模型参数、payload、错误处理和多图语义不清晰。
- 为 Video 新建一套完全不同的流程，导致用户在 Images 里有两种不兼容的 job、history、asset 和 settings 体验。

目标：

- 以 provider-aware 的方式接入 Agnes 图片模型，兼容当前 Grok 图片流程。
- 在 Images 能力域内新增 Agnes 视频生成能力，交互尽量复用现有 job 流程。
- 保留已有 xAI/Grok 默认行为、旧 API、旧数据和旧资产访问路径。
- 让后端从单 xAI client 逐步演进为 provider adapter/catalog，而不是一次性重构成过度抽象平台。
- 新增数据结构必须可向后兼容，可迁移，可回退，不破坏旧 history/library。

## 3. 产品范围

### 3.1 MVP 范围

- Agnes provider 设置：保存、清除、masked 展示 Agnes API Key。
- Agnes 图片模型：
  - `agnes-image-2.1-flash`
  - `agnes-image-2.0-flash`
- Agnes 视频模型：
  - `agnes-video-v2.0`
- 保留废弃模型的 catalog 记录，但默认不在普通模型选择中展示：
  - `agnes-image-1.2`
  - `agnes-video-v1.2`
- 图片生成继续支持当前三类体验：
  - 文生图。
  - 图生图。
  - 多图编辑/组合。
- 视频生成支持：
  - 文生视频。
  - 图生视频。
  - 多图视频。
  - keyframe 动画。
- 视频 job 与图片 job 一样异步创建、可轮询、可恢复、可进入 History。
- 视频结果进入 Library，支持播放、下载、删除、元数据 inspector 和存储位置展示。
- Library 增加 media type 筛选：`All / Images / Videos`。
- History 增加 media type 和 provider 筛选。
- Dashboard 只展示摘要，不展开 Agnes 或 Video 的完整配置。
- 所有 provider 调用、设置变更、任务失败、资产保存失败、资产删除都写 audit 或 task events。

### 3.2 非目标

- 本阶段不接入 Agnes Text 模型。
- 不把 Images 改名为全新的全局 `Media` 一级导航。
- 不新增公开视频分享页、视频作品集、社交相册或发布工作流。
- 不支持视频编辑器、时间线剪辑、字幕轨、音频混合或转码工作台。
- 不自动购买、检测或管理 Agnes 额度。
- 不把 Agnes API Key、完整 prompt、完整远程 URL query、图片 base64、视频下载 URL query 写入日志或 audit。
- 不把 Agnes provider 设置塞进通用 `Settings`；它仍属于 Images 模块。

## 4. Agnes 模型与接口清单

### 4.1 文档目录判断

Agnes 文档目录包含 Text、Image、Video 三组模型。当前阶段只纳入 Image 和 Video。Text 组仅作为后续扩展候选，不参与本设计的 API、UI 或数据迁移。

### 4.2 图片模型

| 文档模型 | API model | 状态 | Endpoint | 能力 | 接入策略 |
| --- | --- | --- | --- | --- | --- |
| Agnes Image 2.1 Flash | `agnes-image-2.1-flash` | 当前 | `POST https://apihub.agnes-ai.com/v1/images/generations` | 文生图、图生图、URL/Base64 输出，高信息密度优化 | 默认 Agnes 图片模型 |
| Agnes Image 2.0 Flash | `agnes-image-2.0-flash` | 当前 | `POST https://apihub.agnes-ai.com/v1/images/generations` | 文生图、图生图、多图组合、URL/Base64 输出 | 作为 Agnes 多图组合优先模型 |
| Agnes Image 1.2 | `agnes-image-1.2` | Deprecated | `POST https://apihub.agnes-ai.com/v1/images/generations` | 旧图片生成 | catalog 保留，默认隐藏 |

图片通用要点：

- `model`、`prompt`、`size` 是 Agnes 图片请求的核心字段。
- 文生图不需要参考图。
- 图生图和多图组合需要输入图片 URL 或 Data URI Base64。
- 文档说明里对 `image` 的位置存在表述不完全一致：文字说明提到 top-level `image` array，但官方示例和错误说明反复使用 `extra_body.image`。实现以示例为准，先通过 `extra_body.image` 发送；如果联调用例证明 top-level 更稳定，再在 Agnes adapter 内做兼容 fallback，不暴露给 UI。
- `response_format` 不应放在 top level，应放入 `extra_body.response_format`。
- 文生图 Base64 输出可用 top-level `return_base64: true`；图生图 Base64 输出使用 `extra_body.response_format = "b64_json"`。
- 输入图片如果不是公网可访问 URL，应使用 Data URI Base64；当前 Library asset 转 provider payload 的逻辑可以复用这个方向。

### 4.3 视频模型

| 文档模型 | API model | 状态 | Create Endpoint | Retrieve Endpoint | 能力 | 接入策略 |
| --- | --- | --- | --- | --- | --- | --- |
| Agnes Video V2.0 | `agnes-video-v2.0` | 当前 | `POST https://apihub.agnes-ai.com/v1/videos` | 推荐 `GET https://apihub.agnes-ai.com/agnesapi?video_id=<VIDEO_ID>`；兼容 `GET /v1/videos/{task_id}` | 文生视频、图生视频、多图视频、keyframe 动画 | 默认 Agnes 视频模型 |
| Agnes Video V1.2 | `agnes-video-v1.2` | Deprecated | `POST https://apihub.agnes-ai.com/v1/videos` | `GET https://apihub.agnes-ai.com/v1/videos/{task_id}` | 旧视频生成 | catalog 保留，默认隐藏 |

视频通用要点：

- 视频生成是异步 task-based API。
- 创建任务返回 `task_id` 和 `video_id`；新接入应优先保存并使用 `video_id` 查询。
- V2.0 的最终视频 URL 在 retrieve response 的 `remixed_from_video_id` 字段中。
- status 至少包含 `queued`、`in_progress`、`completed`、`failed`。
- `num_frames <= 441`，且必须满足 `8n + 1`。
- `frame_rate` 范围为 `1` 到 `60`。
- 常用参数可以预设为：
  - 约 3 秒：`num_frames = 81`、`frame_rate = 24`
  - 约 5 秒：`num_frames = 121`、`frame_rate = 24`
  - 约 10 秒：`num_frames = 241`、`frame_rate = 24`
  - 约 18 秒：`num_frames = 441`、`frame_rate = 24`
- 图生视频可以使用 top-level `image` 字符串。
- 多图视频使用 `extra_body.image` 数组。
- keyframe 动画使用 `extra_body.image` 数组，并设置 `extra_body.mode = "keyframes"`。

## 5. 兼容性原则

### 5.1 Grok 流程不破坏

必须保持以下既有行为：

- 旧的 `Images` 一级导航继续可用。
- `Generate / Library / History / Settings` 结构继续可用。
- 旧的 Grok 模型选项、模式、默认参数和校验不变。
- 旧 API `POST /api/images/jobs` 继续接受现有 multipart form 字段。
- 旧 `image_generation_jobs`、`image_generation_sources`、`image_generation_outputs`、`image_assets` 数据继续可读。
- 旧 Library asset 可以继续作为 Grok 图生图参考图。
- 旧 xAI API Key 设置字段和 masked 展示继续可用。
- 已保存到本地或对象存储的旧图片资产访问、下载、删除和私密访问控制不变。

### 5.2 Agnes 是增量 provider

新增 Agnes 时，不应把现有 `provider = xai` 记录改写成 Agnes，也不应把当前单例 provider 设置直接替换为 Agnes。

建议：

- 旧 settings 继续表示 xAI 默认设置。
- 新增 provider credential/config 表或在 settings 迁移中扩展为多 provider 行。
- job 创建时显式记录 `provider`、`media_type`、`model`、`provider_request_id`。
- 默认 provider 仍可保持 xAI，直到用户在 Settings 中选择 Agnes 或在 Generate 表单中切换 provider。
- provider 不支持的模式在 UI disabled，在后端也必须再次校验。

### 5.3 视频不拆成孤岛

视频能力应复用现有 Images 的概念：

- 同样是 generation job。
- 同样有 sources 和 outputs。
- 同样进入 Library。
- 同样通过 events/SSE 或轮询展示进度。
- 同样使用模块 Settings 管理 provider 和默认参数。
- 同样遵守本地/S3-compatible 存储策略。

但视频资产和图片资产在物理处理上不同：

- 视频文件可能更大，必须流式下载和保存。
- 视频预览需要 `video` player 或 poster，不应按图片缩略图直接假设尺寸。
- 视频下载、删除、归档需要 size limit、timeout 和失败补偿。
- 视频输出不应写入 `image_generation_outputs` 的图片专用字段后再靠空字段凑合，应有清晰 media-aware 结构。

## 6. 信息架构与 UI

### 6.1 一级导航

第一阶段保持一级导航为 `Images`。

理由：

- 当前产品文档和实现已经把 Images 作为独立能力域。
- 用户明确要求整体交互流程尽量与之前一致。
- 直接新增 `Video` 一级导航会把 provider、prompt library、history、storage、asset library 拆散。
- 直接改名 `Media` 会影响旧认知、文档和路由；可以后续在产品文案稳定后再评估。

页面标题可以使用更包容但低噪音的表达，例如：

- 导航：`Images`
- 页面标题：`Images`
- Generate 内 scope：`Image / Video`
- Library 筛选：`Media type`

不建议在主导航里写 `AI Media Studio`、`Creative Lab` 或类似营销化名称。

### 6.2 二级结构

保留现有二级 tab：

- `Generate`：图片/视频生成入口。
- `Library`：图片与视频资产库。
- `History`：图片与视频 job 历史。
- `Prompt Library`：继续保存 prompt 模板，后续可加 media/model tags。
- `Settings`：xAI、Agnes、默认参数和存储设置。

如果当前 UI 已经把 `Prompt Library` 作为 Generate 内部面板而非二级 tab，仍按现状保持，不为 Agnes 单独拆一级入口。

### 6.3 Generate 页面

Generate 主流程：

1. 选择 media scope：`Image` 或 `Video`。
2. 选择 provider：`xAI` 或 `Agnes`。
3. 选择 model。
4. 输入 prompt。
5. 根据 model capability 显示对应参数和参考图 slot。
6. 点击生成，立即创建本地 job。
7. 当前结果面板展示 queued/running/completed/failed 状态。
8. 成功后结果进入 Library 和 History。

布局建议：

- 顶部轻量状态条显示 provider key 状态、最近失败、当前队列。
- 主区域左侧是表单，右侧是当前 job/result inspector；不要在页面中堆叠厚重卡片。
- `Image / Video` 使用小型 segmented control。
- provider 和 model 是紧凑 select，不做大模型卡片。
- 不同 provider 的参数通过同一位置切换，避免 Grok 一套表单、Agnes 一套表单。
- 高级参数收进 collapsible 区域，默认只露出常用项。

图片参数：

- Grok 保持：mode、model、aspect ratio、resolution、n、response format、reference slots。
- Agnes Image 增加或替换：
  - `size`，例如 `1024x768`。
  - `response_format`，映射到 `extra_body.response_format` 或 `return_base64`。
  - reference slots：2.1 默认 0 或 1；2.0 支持 0、1 或多图。
  - `n` 第一阶段可以固定为 1，除非 live test 确认 Agnes 支持多输出数量。

视频参数：

- `video_mode`：`text_to_video`、`image_to_video`、`multi_image_video`、`keyframes`。
- `size` 或 `width`/`height`：默认提供常用 preset，advanced 中允许手动输入。
- `duration_preset`：3s、5s、10s、18s，映射 `num_frames` 和 `frame_rate`。
- `num_frames`：advanced，校验 `8n + 1` 且 `<= 441`。
- `frame_rate`：advanced，范围 `1..60`。
- `seed`：advanced，可选。
- `negative_prompt`：advanced，可选；文档在 V1.2 中出现，V2.0 如未确认支持，应先作为 adapter 内候选字段，UI 默认不展示。
- reference slots：单图视频 1 张；多图/keyframes 2 张起。

生成按钮状态：

- 缺 provider key：disabled，并提示去 `Settings`。
- 当前 provider 不支持所选 media/mode：disabled。
- `num_frames` 不满足规则：disabled，并在字段下方显示简短错误。
- 参考图数量不满足：disabled。
- 网络任务创建后按钮进入 loading，但页面不锁死，用户可切换去 History。

### 6.4 Library 页面

Library 继续是资产中心，不是作品集。

新增能力：

- media type 筛选：`All / Images / Videos`。
- provider 筛选：`All / xAI / Agnes`。
- model 筛选或搜索。
- 视频 tile 使用稳定 aspect-ratio，显示 poster 或首帧占位。
- 视频 tile hover 操作：播放、下载、删除、打开 job。
- 右侧 inspector 根据 media type 切换字段。

视频 inspector 字段：

- asset id。
- job id。
- provider。
- model。
- video mode。
- prompt preview。
- width / height。
- seconds。
- frame rate。
- num frames。
- storage backend。
- size bytes。
- checksum。
- remote URL redacted label。
- provider task id / video id 的短标签。
- status、last error、created/completed time。

视频播放：

- 点击 tile 打开轻量 viewer。
- viewer 左侧是 video player，右侧是 inspector。
- 支持播放/暂停、下载、删除、打开 job。
- 不做时间线剪辑和复杂播放器皮肤。

### 6.5 History 页面

History 仍以 job 为核心。

新增字段和筛选：

- media type：image/video。
- provider：xAI/Agnes。
- model。
- mode。
- status。
- provider task id / video id 短标签。
- duration/progress，仅视频 job 展示。

History 详情：

- 图片 job 展示当前图片输出和 sources。
- 视频 job 展示进度、provider polling 信息、最终视频 asset。
- 失败 job 展示 redacted 错误摘要和可重试入口。
- 旧 Grok job 详情不能因为新增 media fields 而显示大量空字段。

### 6.6 Settings 页面

Settings 仍属于 Images 模块。

建议分组：

- `Providers`
  - xAI：API Key、默认模型、默认图片参数。
  - Agnes：API Key、默认图片模型、默认视频模型。
- `Defaults`
  - default media scope。
  - image defaults。
  - video defaults。
- `Storage`
  - 当前 Images storage settings。
  - 对象存储 profile 和 prefix。
  - fallback 策略。
- `Retention`
  - job retention。
  - asset cleanup 策略。

视觉要求：

- provider key 输入保持 masked。
- 删除/清除 key 是 danger 操作，需要确认。
- 连接测试只显示 success/error 摘要，不显示原始 Authorization header 或完整 provider response。
- 不展示 Agnes 的营销描述、榜单 ELO 或价格卡片；这些不是控制台核心任务。

## 7. 后端设计

### 7.1 Provider Catalog

新增 provider/model capability catalog，前后端共享同一语义，不把 provider 判断散落在 JSX 和 handler 里。

建议 Go 侧结构：

```go
type MediaType string

const (
	MediaTypeImage MediaType = "image"
	MediaTypeVideo MediaType = "video"
)

type ProviderID string

const (
	ProviderXAI   ProviderID = "xai"
	ProviderAgnes ProviderID = "agnes"
)

type ModelCapability struct {
	Provider       ProviderID
	Model          string
	MediaType      MediaType
	Deprecated     bool
	DefaultFor      []string
	SupportedModes []string
	Parameters     ModelParameterSchema
}
```

Catalog 用途：

- UI model select。
- 后端 request validation。
- job 创建时的 capability check。
- Settings 默认模型校验。
- Deprecated 模型默认隐藏，但保留兼容读取。

### 7.2 Provider Adapter

现有 `XAIClient` 可以保留，并在服务层增加轻量 adapter。

建议接口：

```go
type ImageProvider interface {
	GenerateImage(ctx context.Context, request ImageRequest, secret ProviderSecret) (ImageResult, error)
}

type VideoProvider interface {
	CreateVideo(ctx context.Context, request VideoRequest, secret ProviderSecret) (VideoCreateResult, error)
	GetVideo(ctx context.Context, providerJob ProviderVideoJob, secret ProviderSecret) (VideoPollResult, error)
}
```

第一阶段不要强行让 xAI 实现视频接口；xAI 只实现 `ImageProvider`，Agnes 同时实现 `ImageProvider` 和 `VideoProvider`。

Adapter 负责：

- provider-specific endpoint。
- payload mapping。
- response parsing。
- provider error redaction。
- timeout。
- result URL/base64/video URL 抽取。

Service 负责：

- 本地 job 生命周期。
- validation。
- source asset 读取和 Data URI 转换。
- output 下载/保存。
- event/audit。
- retention/cleanup。

### 7.3 图片 payload mapping

xAI 保持当前 mapping：

- 文生图：`/images/generations`。
- 图生图/多图：`/images/edits`。
- `aspect_ratio`、`resolution`、`n`、top-level `response_format` 保持现状。

Agnes Image mapping：

```json
{
  "model": "agnes-image-2.1-flash",
  "prompt": "PROMPT",
  "size": "1024x768",
  "extra_body": {
    "response_format": "url"
  }
}
```

图生图：

```json
{
  "model": "agnes-image-2.1-flash",
  "prompt": "PROMPT",
  "size": "1024x768",
  "extra_body": {
    "image": ["https://example.com/input.png"],
    "response_format": "url"
  }
}
```

文生图 Base64：

```json
{
  "model": "agnes-image-2.1-flash",
  "prompt": "PROMPT",
  "size": "1024x768",
  "return_base64": true
}
```

Agnes response 统一转成现有 `ImageResult`：

- `data[].url` -> remote URL output。
- `data[].b64_json` -> base64 output。
- `data[].revised_prompt` -> revised prompt。
- `created` -> provider created time，可进 metadata。

### 7.4 视频 payload mapping

文生视频：

```json
{
  "model": "agnes-video-v2.0",
  "prompt": "PROMPT",
  "height": 768,
  "width": 1152,
  "num_frames": 121,
  "frame_rate": 24
}
```

图生视频：

```json
{
  "model": "agnes-video-v2.0",
  "prompt": "PROMPT",
  "image": "https://example.com/image.png",
  "num_frames": 121,
  "frame_rate": 24
}
```

多图视频：

```json
{
  "model": "agnes-video-v2.0",
  "prompt": "PROMPT",
  "extra_body": {
    "image": [
      "https://example.com/image1.png",
      "https://example.com/image2.png"
    ]
  },
  "num_frames": 121,
  "frame_rate": 24
}
```

Keyframe：

```json
{
  "model": "agnes-video-v2.0",
  "prompt": "PROMPT",
  "extra_body": {
    "image": [
      "https://example.com/keyframe1.png",
      "https://example.com/keyframe2.png"
    ],
    "mode": "keyframes"
  },
  "num_frames": 121,
  "frame_rate": 24
}
```

创建响应要保存：

- provider task id。
- provider video id。
- status。
- progress。
- seconds。
- size。

轮询优先：

```text
GET https://apihub.agnes-ai.com/agnesapi?video_id=<VIDEO_ID>
```

兼容 fallback：

```text
GET https://apihub.agnes-ai.com/v1/videos/<TASK_ID>
```

完成后：

- `remixed_from_video_id` 作为最终视频远程 URL。
- 下载并保存为 video asset。
- 若下载失败，保留 redacted remote URL label 和 failed asset event，不让 job 成功状态丢失。

### 7.5 Job 生命周期

图片 job：

- 继续复用现有 queued/running/success/failed/interrupted 语义。
- Agnes 图片请求仍可以在后台 worker 内执行，完成后保存 output。

视频 job：

- create request 返回本地 job id。
- worker 创建 Agnes video task。
- 本地 job 进入 `running` 或 `provider_queued`。
- poller 周期查询 provider。
- progress 变化写 task events，但要限频，避免高频日志。
- completed 后下载视频并保存 asset。
- failed 后写 redacted error。
- 服务重启时恢复未完成视频 job 的 polling；如果无法安全恢复，标记 interrupted 并写 event。

状态映射：

| Agnes status | 本地 job status | 说明 |
| --- | --- | --- |
| `queued` | `running` 或 `provider_queued` | 已提交 provider，等待生成 |
| `in_progress` | `running` | 更新 progress |
| `completed` | `success` | 下载/保存 output 后完成 |
| `failed` | `failed` | 保存错误摘要 |
| unknown | `running` 或 `failed` | 按可恢复性判断，记录 provider status |

## 8. 数据结构设计

### 8.1 Provider settings

当前 `image_provider_settings` 是单例，并且已有 xAI 字段。为兼容旧数据，有两种可选方案。

推荐方案 A：新增多 provider settings 表，旧表保留兼容读取。

```sql
CREATE TABLE IF NOT EXISTS media_provider_settings (
  provider TEXT PRIMARY KEY,
  enabled INTEGER NOT NULL DEFAULT 0,
  api_key TEXT NOT NULL DEFAULT '',
  api_key_masked TEXT NOT NULL DEFAULT '',
  default_image_model TEXT NOT NULL DEFAULT '',
  default_video_model TEXT NOT NULL DEFAULT '',
  default_image_params_json TEXT NOT NULL DEFAULT '{}',
  default_video_params_json TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
```

兼容策略：

- 迁移时不删除 `image_provider_settings`。
- xAI 读取优先从新表读取；如果新表没有 xAI 行，则从旧表回填运行态 settings。
- 写入 xAI settings 时可以双写一段兼容期，或先保持旧 API 写旧表、新 provider 写新表。
- 后续确认兼容期结束后再收敛。

备选方案 B：扩展 `image_provider_settings` 为多列。

不推荐，因为 xAI 和 Agnes 的默认参数差异较大，视频 defaults 会让旧单例表变得含混。

### 8.2 Job 表

有两个实现方向。

推荐方向：新增 media-aware job 表，旧 image job 表保留。

```sql
CREATE TABLE IF NOT EXISTS media_generation_jobs (
  id TEXT PRIMARY KEY,
  media_type TEXT NOT NULL,
  provider TEXT NOT NULL,
  status TEXT NOT NULL,
  mode TEXT NOT NULL,
  mode_label TEXT NOT NULL DEFAULT '',
  model TEXT NOT NULL,
  endpoint TEXT NOT NULL DEFAULT '',
  prompt TEXT NOT NULL,
  parameters_json TEXT NOT NULL DEFAULT '{}',
  source_count INTEGER NOT NULL DEFAULT 0,
  output_count INTEGER NOT NULL DEFAULT 0,
  provider_task_id TEXT NOT NULL DEFAULT '',
  provider_video_id TEXT NOT NULL DEFAULT '',
  provider_status TEXT NOT NULL DEFAULT '',
  progress INTEGER NOT NULL DEFAULT 0,
  usage_json TEXT NOT NULL DEFAULT '{}',
  error_message TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  started_at TEXT,
  completed_at TEXT
);
```

旧 `image_generation_jobs` 继续服务旧 API 和旧页面数据。前端 History 查询可以由后端聚合旧 image jobs 和新 media jobs，或在实现阶段把新 Agnes image jobs 也写入新表，并让新 History API 读新旧两套。

如果实现希望最小改动，可以先给 `image_generation_jobs` 增加 `media_type`、`parameters_json`、`provider_task_id`、`provider_video_id`、`progress` 等字段。但这会让表名和语义继续绑定 image，不利于视频长期维护。

### 8.3 Source 与 output

新增 media-aware source/output 表：

```sql
CREATE TABLE IF NOT EXISTS media_generation_sources (
  id TEXT PRIMARY KEY,
  job_id TEXT NOT NULL,
  asset_id TEXT NOT NULL DEFAULT '',
  slot INTEGER NOT NULL,
  source_type TEXT NOT NULL,
  source_role TEXT NOT NULL DEFAULT '',
  mime_type TEXT NOT NULL DEFAULT '',
  size_bytes INTEGER NOT NULL DEFAULT 0,
  url_redacted TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS media_generation_outputs (
  id TEXT PRIMARY KEY,
  job_id TEXT NOT NULL,
  asset_id TEXT NOT NULL DEFAULT '',
  slot INTEGER NOT NULL,
  media_type TEXT NOT NULL,
  remote_url_redacted TEXT NOT NULL DEFAULT '',
  local_name TEXT NOT NULL DEFAULT '',
  mime_type TEXT NOT NULL DEFAULT '',
  revised_prompt TEXT NOT NULL DEFAULT '',
  storage TEXT NOT NULL DEFAULT '',
  size_bytes INTEGER NOT NULL DEFAULT 0,
  metadata_json TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL
);
```

### 8.4 Asset 表

当前 `image_assets` 可以短期继续保存图片资产。视频接入建议新增 `media_assets`，而不是让视频挤进 `image_assets`。

```sql
CREATE TABLE IF NOT EXISTS media_assets (
  id TEXT PRIMARY KEY,
  media_type TEXT NOT NULL,
  asset_type TEXT NOT NULL,
  status TEXT NOT NULL,
  private INTEGER NOT NULL DEFAULT 0,
  provider TEXT NOT NULL DEFAULT '',
  model TEXT NOT NULL DEFAULT '',
  job_id TEXT NOT NULL DEFAULT '',
  source_role TEXT NOT NULL DEFAULT '',
  slot INTEGER NOT NULL DEFAULT 0,
  prompt_preview TEXT NOT NULL DEFAULT '',
  revised_prompt_preview TEXT NOT NULL DEFAULT '',
  original_filename TEXT NOT NULL DEFAULT '',
  original_source_redacted TEXT NOT NULL DEFAULT '',
  mime_type TEXT NOT NULL DEFAULT '',
  extension TEXT NOT NULL DEFAULT '',
  size_bytes INTEGER NOT NULL DEFAULT 0,
  width INTEGER NOT NULL DEFAULT 0,
  height INTEGER NOT NULL DEFAULT 0,
  duration_seconds REAL NOT NULL DEFAULT 0,
  frame_rate INTEGER NOT NULL DEFAULT 0,
  frame_count INTEGER NOT NULL DEFAULT 0,
  checksum_sha256 TEXT NOT NULL DEFAULT '',
  local_name TEXT NOT NULL DEFAULT '',
  storage_backend TEXT NOT NULL DEFAULT '',
  object_storage_profile_id TEXT NOT NULL DEFAULT '',
  s3_bucket TEXT NOT NULL DEFAULT '',
  s3_region TEXT NOT NULL DEFAULT '',
  s3_endpoint_label TEXT NOT NULL DEFAULT '',
  s3_key TEXT NOT NULL DEFAULT '',
  s3_etag TEXT NOT NULL DEFAULT '',
  private_at TEXT,
  archived_at TEXT,
  deleted_at TEXT,
  deleted_reason TEXT NOT NULL DEFAULT '',
  last_error TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
```

兼容策略：

- 旧 `image_assets` 不迁移也可继续读。
- 新 Library API 可以聚合 `image_assets` 和 `media_assets`。
- 新 Agnes 图片也可以写入 `media_assets`；如果为了减少 UI 变更需要和旧 Library 共用，可以在兼容期为图片输出双写 `image_assets` 和 `media_assets`，但必须保证删除和私密状态同步。
- 更稳妥的实现路径是先让 Library 查询层做聚合，不做双写。

## 9. API 设计

### 9.1 兼容旧 API

旧 API 保持：

- `GET /api/images/status`
- `GET /api/images/settings`
- `PUT /api/images/settings`
- `POST /api/images/jobs`
- `GET /api/images/jobs`
- `GET /api/images/jobs/{id}`
- Library 现有图片 asset API。

旧 `POST /api/images/jobs` 的默认 provider 仍是 xAI；只有新增字段显式传 `provider=agnes` 时才走 Agnes。

### 9.2 新增/扩展 API

建议新增 media-aware API，UI 新能力走新 API，旧 UI 可继续用旧 API：

| Method | Path | 用途 |
| --- | --- | --- |
| `GET` | `/api/images/providers` | 返回 provider/model capability catalog 和 masked key 状态 |
| `PUT` | `/api/images/providers/{provider}` | 更新 provider key 与默认参数 |
| `POST` | `/api/images/generations` | 创建 image/video generation job |
| `GET` | `/api/images/generations` | 查询聚合 job 列表 |
| `GET` | `/api/images/generations/{id}` | 查询 job 详情 |
| `POST` | `/api/images/generations/{id}/retry` | 按原参数重试 |
| `POST` | `/api/images/generations/{id}/cancel` | 尽力取消本地 job 或停止 polling |
| `GET` | `/api/images/assets` | 查询聚合 image/video asset |
| `GET` | `/api/images/assets/{id}/content` | 读取图片或视频内容 |
| `GET` | `/api/images/assets/{id}/download` | 下载图片或视频 |
| `DELETE` | `/api/images/assets/{id}` | 删除图片或视频 |

请求体示例：

```json
{
  "mediaType": "video",
  "provider": "agnes",
  "mode": "image_to_video",
  "model": "agnes-video-v2.0",
  "prompt": "Animate the subject with subtle camera movement while preserving identity",
  "parameters": {
    "width": 1152,
    "height": 768,
    "numFrames": 121,
    "frameRate": 24
  },
  "sources": [
    {
      "type": "asset",
      "assetId": "asset_example"
    }
  ]
}
```

旧 multipart form 支持策略：

- 保留旧字段。
- 新字段可以追加：`provider`、`media_type`、`video_mode`、`size`、`width`、`height`、`num_frames`、`frame_rate`。
- 后端不依赖前端隐藏字段做安全判断，所有参数重新校验。

## 10. 存储与资产处理

### 10.1 图片

Agnes 图片输出处理复用当前 Images 资产逻辑：

- URL 输出：后端下载图片，校验 MIME/size，保存本地或对象存储。
- Base64 输出：后端解码，校验 MIME/size，保存。
- 保存失败时 job 不应丢失 provider 原始结果摘要；output 记录进入 degraded 状态。
- 输入 Library asset 由后端读取 bytes 并转 Data URI，不把本地 authenticated asset URL 传给 Agnes。

### 10.2 视频

视频新增要求：

- 后端流式下载，不一次性读入内存。
- 设置单视频最大下载大小，MVP 可先保守设为 512 MB，后续放到 Settings。
- 设置下载超时和总任务超时。
- MIME 初始允许 `video/mp4`，如果 provider 返回其他视频 MIME，先进入 unsupported 状态并记录错误摘要。
- 本地文件权限沿用受控资产目录策略。
- 对象存储上传失败按 Images 现有 fallback 策略处理。
- 删除视频必须先标记 DB，再执行物理删除或用补偿任务处理部分失败。
- 私密图库规则同样适用于视频资产。

### 10.3 URL 与日志脱敏

所有 provider URL 和错误信息进入日志/audit/events 前必须处理：

- 删除 query 和 fragment。
- 裁剪长度。
- redaction Authorization、api key、token、signature、cookie 等模式。
- 不记录 base64/data URL 正文。
- 不记录完整 prompt 到 service log；prompt 只进入受控 job/history 存储和前端 authenticated response。

## 11. 校验规则

### 11.1 通用

- provider 必须存在于 catalog。
- model 必须属于 provider 且支持 media type。
- deprecated model 默认拒绝普通创建；仅允许通过显式兼容开关或历史重试进入。
- prompt trim 后不能为空。
- prompt 最大长度沿用现有 8000 字符，后续按 provider 文档调整。
- API Key 未配置时不能创建 provider job。
- source asset 必须属于当前 owner session 可访问范围；私密 asset 必须已解锁。

### 11.2 Agnes 图片

- `size` 必须符合 `WIDTHxHEIGHT`，宽高为正整数。
- 默认 size：`1024x768`。
- `response_format` 只允许 `url`、`b64_json`。
- `agnes-image-2.1-flash`：
  - 文生图：0 张参考图。
  - 图生图：1 张参考图。
  - 多图不作为默认可选模式，除非 live test 确认。
- `agnes-image-2.0-flash`：
  - 文生图：0 张参考图。
  - 图生图：1 张参考图。
  - 多图组合：2 到 3 张参考图，MVP 与 Grok 多图 slot 数保持一致。
- 输入 URL 允许 `http`、`https`、`data:image/`。
- 上传图片大小沿用当前单张 12 MB、总 multipart 40 MB；若 Agnes 实测限制更低，在 adapter validation 中收紧。

### 11.3 Agnes 视频

- `video_mode`：
  - `text_to_video`：0 张参考图。
  - `image_to_video`：1 张参考图。
  - `multi_image_video`：2 到 3 张参考图。
  - `keyframes`：2 到 3 张参考图。
- `num_frames <= 441`。
- `num_frames % 8 == 1`。
- `frame_rate` 范围 `1..60`。
- `width` 和 `height` 为正整数，默认使用 preset。
- 计算 `seconds = num_frames / frame_rate`，写入 job metadata。
- 参考图仍按图片 input 限制处理，转为公网 URL 或 Data URI。

## 12. 事件、审计与日志

### 12.1 Task events

写入 task/session events：

- job created。
- provider request started。
- provider task created。
- provider polling status changed。
- provider completed。
- output download started/completed/failed。
- asset saved。
- job failed/interrupted。

视频 polling 事件必须限频：

- progress 不变化时不重复写。
- progress 高频变化时按百分比变化或时间窗口采样。
- SSE heartbeat 不进入 service log。

### 12.2 Audit

写入 audit：

- Agnes API Key 设置、清除、连接测试。
- 创建 Agnes 图片/视频 job。
- 视频任务取消/重试。
- 图片/视频资产删除。
- 私密状态变更。
- 对象存储归档失败或配置变更。

Audit 中不记录：

- API Key 明文。
- Authorization header。
- 完整 provider response。
- 完整 base64。
- 带 query 的远程 URL。

### 12.3 Service log

只记录关键边界：

- provider request failed。
- provider polling failed。
- output download failed。
- storage failed。
- migration failed。
- panic/5xx/慢请求。

成功路径保持克制，不记录每次成功轮询和完整 stdout/prompt。

## 13. 迁移策略

### 13.1 数据迁移

迁移必须是 additive：

1. 新增 provider catalog 代码，不修改旧数据。
2. 新增 provider settings 表或字段。
3. 新增 media job/source/output/asset 表。
4. 新 API 聚合新旧 job 和 asset。
5. UI 逐步切到 new API，但旧 API 保留。
6. 验证旧 Grok 生成、旧 Library、旧 History 正常后，再启用 Agnes provider。

不得做：

- 删除旧 `image_provider_settings`。
- 把所有旧 job 一次性改写到新表。
- 修改旧 asset local name 或 S3 key。
- 更改旧图片下载 URL 的访问控制语义。

### 13.2 回退策略

如果 Agnes 接入出现问题：

- 可以通过 Settings 禁用 Agnes provider。
- xAI/Grok flow 继续工作。
- 已创建 Agnes job 保留 history。
- 未完成 video polling job 可以标记 interrupted，不删除 sources。
- 已保存 video asset 继续可读、可下载、可删除。
- 新表存在不影响旧代码读取旧表。

### 13.3 Deprecated 模型

Deprecated 模型处理：

- catalog 记录存在。
- 普通 UI 不展示。
- 历史记录能显示。
- 旧 job 重试默认切换到当前模型，并提示模型已 deprecated。
- 若用户通过兼容开关显式选择 deprecated 模型，必须在 UI 和 job metadata 中标记。

## 14. 分阶段实现

### P0：Provider catalog 与 Agnes 图片

- 增加 provider/model capability catalog。
- 扩展 Settings 支持 Agnes API Key。
- Agnes Image adapter。
- Generate 支持 provider-aware image params。
- Agnes image job 写入现有或新 job 表。
- Library/History 正常展示 Agnes 图片结果。
- 保持 Grok image flow 全量回归。

验收：

- 旧 Grok 文生图/图生图/多图仍可用。
- Agnes 2.1 文生图、图生图可用。
- Agnes 2.0 多图组合可用。
- API Key masked/audit 正常。
- `response_format` mapping 不触发 top-level 400。

### P1：Agnes 视频 job 与 polling

- 新增 video request validation。
- Agnes Video adapter。
- video create + poller。
- video job 状态和 progress。
- video result 下载保存。
- History 详情展示视频 job。
- Library 视频 asset 基础展示、播放、下载、删除。

验收：

- 文生视频创建、轮询、完成、保存。
- 图生视频使用 Library image asset 作为 source。
- 多图/keyframes 基本可用。
- 服务重启后未完成 video job 可恢复或安全 interrupted。
- provider 失败、下载失败、存储失败都有可读错误和审计。

### P2：Library 与 Prompt Library 打磨

- Library media type/provider/model 筛选。
- 视频 viewer 体验完善。
- Prompt Library 增加 media/model tags。
- 重试时自动带入原 provider/model/params。
- Settings 默认视频参数。
- Retention 和 cleanup 覆盖视频。

验收：

- 图片/视频资产在同一 Library 中可扫描但不混乱。
- inspector 字段按 media type 精简。
- 删除、私密、归档、下载的权限和失败分支一致。

## 15. 测试计划

后端单测：

- provider catalog model/mode validation。
- xAI 旧 request validation 回归。
- Agnes image payload mapping。
- Agnes image response parsing：URL、Base64、empty data、provider error。
- Agnes video create response parsing。
- Agnes video poll response parsing：queued、in_progress、completed、failed、unknown。
- `num_frames` 规则。
- source asset -> Data URI 转换。
- provider error redaction。
- settings masked/audit。
- migration additive，不破坏旧 rows。

集成测试：

- 旧 `POST /api/images/jobs` 创建 xAI image job。
- 新 `POST /api/images/generations` 创建 Agnes image job。
- 新 API 创建 Agnes video job，mock poll completed。
- output 下载失败时 job/event/asset 状态正确。
- S3 fallback 与本地 fallback。
- 私密 asset 作为 reference 时必须 unlock。

前端验证：

- Grok flow 参数与旧 UI 一致。
- 切换 Agnes Image 后只展示 Agnes 支持参数。
- 切换 Video 后出现 duration/size/reference 参数。
- disabled/error/loading/empty 状态完整。
- History 和 Library 筛选不破坏旧图片。
- 桌面端工作台布局稳定，文本不溢出，不出现营销页式 hero 或大装饰。

## 16. 风险与待确认

- Agnes 图片文档对 `image` 字段位置有不一致描述。实现先按官方示例使用 `extra_body.image`，上线前必须用真实 API 做最小联调确认。
- Agnes Image 2.1 文档没有像 2.0 一样明确展示多图组合能力；MVP 不默认给 2.1 开多图。
- Agnes Video V2.0 的最终视频 URL 字段名是 `remixed_from_video_id`，命名不像 URL，adapter 必须用测试锁定。
- 视频文件大小、下载耗时、对象存储上传失败会比图片更频繁，必须有 timeout、size limit 和 fallback。
- Provider 价格和额度可能变化，控制台不应硬编码价格展示；如未来需要成本估算，应单独设计并允许手动配置。
- 如果后续把 `Images` 重命名为 `Media`，需要做路由 alias、文档更新和 Dashboard 文案迁移，不应和本次接入耦合。

## 17. 实现建议摘要

建议从小而稳的路径开始：

1. 保持 `Images` 一级域和旧 Grok flow 不变。
2. 增加 provider/model capability catalog。
3. 先接 Agnes Image，验证 payload 差异和 asset 保存链路。
4. 再接 Agnes Video，使用同一 job/history/library 语言。
5. 新数据结构 additive，旧数据只读兼容，不做破坏性迁移。
6. UI 始终保持 Quiet Agent Workbench 风格，把复杂参数放进低噪音 advanced 区域。

