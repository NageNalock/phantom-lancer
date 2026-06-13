# 多媒体资源库与对象存储功能设计

文档日期：2026-06-05  
关联文档：

- [grokbox-module-feature-design.md](./grokbox-module-feature-design.md)
- [personal-web-terminal-product-features.md](./personal-web-terminal-product-features.md)
- [personal-web-terminal-technical-design.md](./personal-web-terminal-technical-design.md)

## 1. Design Read

Reading this as: 个人服务器控制台里的多媒体资源资产管理工作台，面向单 owner 技术用户，采用 Quiet Agent Workbench / Quiet DevOps Control Plane 语言，强调生成结果可检索、可恢复、可删除、可下载、存储位置可解释，以及低噪音的右侧元数据 inspector。

资源库不是营销式作品集、社交相册或瀑布流灵感墙。它应服务多媒体模块的生成、排查、归档和清理任务：左侧保持全局导航，多媒体内部使用二级 tab；主工作区展示可扫描的图片/视频资产；右侧 inspector 展示当前选中资源的元数据、存储位置和风险状态。底层 API、表名和事件前缀可以继续使用 `images` 以兼容历史实现。

## 2. 背景与目标

当前多媒体模块已经支持 Grok Imagine generation job、Agnes 图片/视频任务、历史记录和本地/对象存储资产读取。历史视图以 job 为中心，不能高效管理每次生成出来的资源，也不能复查用户上传过的参考图。随着生成次数增加，Owner 需要一个以资源资产为中心的资源库：

- 从所有历史 job 中查看已生成图片和视频。
- 查看用户上传过的参考图，并知道它们被哪些 job 使用过。
- 点击图片放大查看或播放视频，并在同一上下文里看到元数据。
- 下载图片或视频。
- 删除不需要的资源，释放本地磁盘或对象存储空间。
- 知道每个资源来自哪个 job、哪个提示词、哪个模型、存储在哪里。
- 当配置对象存储后，生成结果优先保存到 S3 兼容对象存储，降低服务器本地磁盘长期占用。
- 已存在本地的资源支持手动归档到对象存储。

本设计只覆盖单 owner 个人服务器场景，不做团队图库、公开分享页、评论协作或多租户权限。

## 3. 产品边界

### 3.1 MVP 范围

- 多媒体内新增 `Library` 二级 tab。
- 资源库展示所有未删除的图片/视频资产，包含生成输出资源和用户上传参考图，默认按创建时间倒序。
- 支持图片缩略图网格、列表密度切换可以后置。
- 支持点击图片打开放大查看器。
- 支持单资源下载。
- 支持单资源删除。
- 支持将图片或视频加入或移出私密收藏夹。
- 进入私密收藏夹必须输入 owner 登录密码；解锁只在当前 Web session 内短期有效。
- 私密资源默认不出现在普通资源库；查看、下载、删除、归档和移出私密收藏夹都必须先解锁。
- 支持右侧 inspector 展示当前选中资源的核心元数据。
- 支持从资源跳转到所属 generation job / History 详情。
- 支持在 Library 中手动上传图片资产，上传前按图片内容 checksum 去重，命中已存在公开资产时复用，不重复写本地或 S3。
- 支持将 Library 中的图片快捷用于 `Generate` 的图生图、图生视频、多图编辑和关键帧参考；后端负责把受控 Library asset 转换为 provider 可用的 payload，不把需要登录态的本地 API URL 直接交给 provider。
- 支持本地图片资产手动归档到 S3。
- 多媒体 Settings 内新增存储设置，默认 `local`。
- 支持 S3 兼容对象存储配置：bucket、region、endpoint、prefix、path style、access key、secret key。
- 生成 job 完成后优先通过 S3 SDK 或对象存储 profile 上传资源；失败时按策略回退到本地保存并记录事件。
- S3 bucket 可以保持私有，不要求公开读。
- 资源读取、下载、删除都需要 owner session；删除和存储设置变更必须校验 CSRF。
- 删除、对象存储配置变更、对象存储上传失败必须写 audit。

### 3.2 非目标

- 不做公开图片分享链接。
- 不做团队共享图库、多用户授权或图库协作。
- 不做图片编辑器、视频时间线、裁剪、标注或二次绘图。
- 不做复杂 DAM 功能，例如版权流转、审核工作流、发布渠道管理。
- 不把多媒体存储设置放进全局 Settings；它属于多媒体模块自己的能力域。
- 不在 Dashboard 展开资源库完整配置；Dashboard 只显示摘要和跳转。
- 不在 S3 对象 metadata 中保存完整 prompt 或 API Key 等敏感信息。

## 4. 信息架构

多媒体作为一级导航能力存在。资源库是多媒体内部的二级视图，不能提升为全局一级导航。旧 `images` 路由/API/event 前缀作为兼容层保留。

多媒体内部二级结构建议：

- `Generate`：生成任务、参数和当前结果。
- `Library`：以图片/视频资源为中心管理生成资产。
- `History`：以 job 为中心查看调用记录、失败原因和参数摘要。
- `生成预设`：保存提示词、模型、模式和常用参数组合；默认不保存参考图引用。
- `Settings`：xAI / Agnes provider 设置、默认生成参数、历史保留策略和存储设置。

`Library` 与 `History` 的区别：

- `Library` 管理资源资产，核心对象是 media/image asset。资产可以是生成输出图、视频，也可以是用户上传参考图。
- `History` 管理一次调用记录，核心对象是 generation job。
- 删除资源不应删除 job 历史；job 仍保留提示词、参数、状态和 audit 上下文。
- 删除 job 历史时可以选择级联删除资源资产，但这是另一个危险操作，不在资源库单资源删除流程里默认触发。
- 删除用户上传参考图不应破坏历史记录；History 中仍保留 source slot、文件摘要和 redacted 来源信息，但资源内容显示为 deleted。

## 5. 资源库主界面交互

### 5.1 桌面布局

桌面端使用工作台布局：

- 顶部：Images 二级 tab 和轻量状态条。
- 主区域：资源库 toolbar + 图片/视频资产网格。
- 右侧：常驻 inspector，宽度约 320px，展示选中资源元数据。

主区域不应使用大欢迎 hero、营销文案、装饰插画或彩色 dashboard 卡片。资源 tile 可以是独立卡片，但页面分区不要再包一层厚重卡片。

### 5.2 Toolbar

Toolbar 放在资源网格上方，保持低噪音和可扫描：

- 搜索：按 prompt、revised prompt、model、job id、asset id、原始文件名摘要查询。
- 常驻筛选：媒体类型 `All / Images / Videos` 和排序。
- 高级筛选：provider、存储位置、生成模式、私密状态等应优先收进 popover 或折叠区，并在折叠状态显示 active filter 摘要。
- 筛选：资产类型 `All / Generated / Uploaded source`。
- 筛选：存储位置 `All / Local / S3 / Remote fallback`。
- 筛选：生成模式 `All / Text to image / Image to image / Multi image edit`。
- 筛选：状态 `Available / Missing / Deleted`，MVP 默认只展示 `Available`。
- 排序：`Newest first`、`Oldest first`、后续可加 `Size`。
- 操作：刷新、下载选中、删除选中、归档到对象存储。批量操作只在选择模式下显示，失败项应保留选中并展示失败摘要。
- 操作：手动上传图片。上传入口应保持低噪音，优先使用 `上传资源` 按钮展开 inline panel 或 drawer，不做独立一级入口，不长期挤占资源网格首屏。
- 操作：将选中图片作为参考使用，支持图生图、图生视频、多图编辑和关键帧。该操作应切换到 `Generate` 并把图片填入对应参考图槽位，用户仍需确认 prompt 和参数后再提交。
- 视图切换：普通资源库 / 私密收藏夹。私密收藏夹切换后先展示解锁面板，解锁成功才加载资源。

按钮规则：

- 下载、删除、刷新使用图标按钮并提供 tooltip；危险删除使用红色语义态。
- 归档到对象存储使用常规次级按钮或上传/云图标，只有 `local` 资产且对象存储已配置时可用。
- 不使用 emoji 表达状态。
- 不使用彩色胶囊堆叠造成视觉噪音。
- 搜索输入和筛选控件高度保持一致，避免 toolbar 换行后拥挤。
- 私密收藏夹入口应使用低噪音 segmented control 或二级按钮，不提升为一级导航，不做大面积 warning 面板。

### 5.3 资源网格

网格是资源库的核心视图。

布局要求：

- 使用稳定的 CSS grid，tile 有固定 `aspect-ratio`，图片加载前后不能改变布局高度。
- 默认 4 到 6 列随容器自适应，移动端降为 2 列或单列列表。
- 图片使用 `object-fit: cover` 填充 tile；视频使用稳定 `aspect-ratio`、poster 或首帧占位；查看器中再显示完整比例或视频播放器。
- hover 显示轻量操作层：查看、下载、删除。
- selected 状态使用细边框或低对比背景，不使用高饱和大片色块。
- 缩略图加载中使用与 tile 尺寸一致的 skeleton。
- 图片缺失、S3 临时不可用、远程 fallback 失效时展示明确但克制的错误态。

tile 信息密度：

- 常驻只显示必要信息，例如时间、存储位置小标识、资产类型短标签、生成模式短标签。
- 生成输出图和用户上传参考图必须可区分，但状态标识要低调，避免把 tile 做成彩色 dashboard。
- prompt、模型、尺寸、完整 storage key 等信息放到右侧 inspector。
- 长文本不覆盖图片主体，避免 prompt 文案遮挡缩略图。

### 5.4 空状态

没有图片时，Library 显示克制的空状态：

- 简短说明：还没有生成图片。
- 主操作：跳转 `Generate`。
- 次操作：打开 `Settings` 检查 provider / storage。

空状态不使用大插画、大标题、营销 CTA 或大面积欢迎区。

### 5.5 私密收藏夹

私密收藏夹是图片库里的受控视图，不是单独图库目录，也不复制图片文件。图片通过 `private` 标记进入私密收藏夹，仍然保留原 asset id、job 关联、存储位置和审计上下文。

交互要求：

- 普通图片库默认只展示非私密图片，避免刷新或重新进入页面时暴露私密资产。
- 私密收藏夹入口放在 Library 内部 toolbar 附近，表达为当前视图切换。
- 未解锁时只展示密码输入面板，不加载私密图片列表、缩略图、元数据或总数。
- 解锁密码使用 owner 当前登录密码，不新增第二套长期密码，避免产生恢复和轮换负担。
- 解锁成功后仅对当前 Web session 生效，并设置短 TTL；过期后重新进入私密收藏夹需要再次输入密码。
- 提供 `锁定` 操作，Owner 可主动清除当前 session 的私密解锁状态。
- 图片 tile、放大查看器和 inspector 都提供 `设为私密` / `移出私密` 操作；移出私密必须在已解锁状态下完成。
- 将图片设为私密后，它应立即从普通图片库视图移出；私密收藏夹解锁后才能看到。

安全语义：

- 私密图片内容读取、下载、删除、归档到 S3、查看单图详情和通过旧本地 asset URL 读取，都必须校验私密解锁状态。
- `privacy=private` 和 `privacy=all` 这类列表查询必须校验私密解锁状态。
- 解锁失败应复用登录失败限流思路，至少包含 IP 维度 backoff，避免密码暴力尝试。
- 解锁、锁定、加入私密、移出私密、限流和 backoff 都要写 audit。
- 私密状态只影响 Phantom Lancer 的访问控制；本地文件路径和 S3 object key 不应因为设为私密而改名或暴露额外语义。

## 6. 放大查看器

点击图片 tile 打开放大查看器。查看器应是任务工具，不是沉浸式相册。

### 6.1 布局

桌面端：

- 左侧大图预览区域，使用深浅中性背景，不使用渐变或毛玻璃装饰。
- 右侧详情栏复用 inspector 信息，保持元数据连续。
- 顶部或右上角提供关闭、下载、删除、打开 job。

移动端：

- 图片优先铺满可用宽度。
- 元数据进入底部 drawer 或折叠详情。
- 操作按钮固定在安全区域内，避免遮挡图片关键区域。

### 6.2 常用操作

- `Esc` 关闭。
- 左右方向键切换上一张 / 下一张。
- `+` / `-` 或按钮控制缩放。
- `Fit` / `Actual size` 切换适配和原始大小。
- 下载当前图片。
- 删除当前图片。
- 打开所属 job 详情。

### 6.3 删除流程

删除是危险操作，必须明确确认。

交互要求：

- 单图删除弹出 confirmation dialog。
- 对话框显示缩略图、创建时间、存储位置和简短 prompt 摘要。
- 主按钮使用 danger 语义，文案为 `删除图片`。
- 删除成功后，查看器自动切到下一张可用图片；没有下一张则关闭查看器并回到网格。
- 删除失败时保留当前图片，展示可重试错误，不提前从列表移除。

数据语义：

- 删除图片只删除 image asset，不删除 generation job。
- DB 中应记录 `deleted_at` 和 `deleted_reason`，用于审计和避免历史引用断裂。
- 本地资产删除文件；S3 资产调用 `DeleteObject`；远程 fallback 资产只能标记删除，因为远程源不归本系统管理。
- 如果物理删除成功但 DB 更新失败，后台需要补偿或在下一次启动时检查孤儿对象。
- 如果 DB 标记成功但物理删除失败，记录 `images.asset.delete_failed`，并在 inspector 显示清理失败状态。

### 6.4 下载流程

下载必须通过后端受控入口，不直接暴露服务器路径或 S3 凭证。

交互要求：

- 单图下载直接触发浏览器下载。
- 文件名建议：`phantom-image-<created-date>-<asset-short-id>.<ext>`。
- S3 图片可由后端生成短 TTL presigned URL，也可由后端代理流式下载；前端不接触 access key。
- 下载失败显示 toast，并在 inspector 中保留当前状态。

批量下载可以作为后续能力：选中多张后由后端生成临时 zip，或逐个触发下载。MVP 不强制实现。

## 7. 右侧 Metadata Inspector

右侧 inspector 是图片库交互的关键，不应变成杂乱的调试 dump。默认按信息重要性分组。

### 7.1 未选中状态

未选中图片时显示图片库摘要：

- 图片总数。
- 本地 / S3 / remote fallback 数量。
- 最近生成时间。
- 当前 storage backend。
- 最近一次存储失败摘要。
- 快捷入口：Generate、Settings。

### 7.2 选中图片状态

选中图片后展示：

基础信息：

- 预览缩略图或小尺寸预览。
- asset id。
- asset type：`generated` / `source_upload` / 后续 `source_url_snapshot`。
- job id。
- slot。
- 创建时间。
- 更新时间。
- 生成模式。
- provider。
- model。
- asset status：available / missing / deleted / archived。

生成参数：

- prompt 摘要，支持复制。
- revised prompt，若存在则显示并支持复制。
- aspect ratio。
- resolution。
- response format。
- image count。
- source count。
- usage 摘要。

来源与关联：

- source role：output / input reference。
- source slot。
- 被哪些 job 使用过，MVP 至少显示当前 job，后续支持引用列表。
- 原始上传文件名摘要，保留 basename 并做长度截断，不展示本地临时路径。
- 原始上传 MIME。
- 原始上传 size。
- 原始上传时间。
- 原始 source URL 的 redacted host/path 摘要，若来源是 URL。
- source URL 是否已被本地/S3 snapshot 固化。
- 上传来源的 digest，用于识别重复上传。

文件信息：

- MIME type。
- size bytes，使用可读格式。
- width / height，若已探测。
- pixel count。
- aspect ratio，按实际 width / height 推导。
- color profile，若可读取。
- EXIF orientation，若可读取。
- dominant color / blurhash / average color，后续可用于缩略图占位。
- checksum，建议 SHA-256。
- perceptual hash，后续可用于近似重复图识别。
- original filename，若存在，只显示安全 basename。
- extension。
- thumbnail status。
- storage backend：`local`、`s3`、`remote`。
- local name 或 S3 object key。技术值使用 monospace，长值中间截断。
- bucket / region / endpoint label。不要显示 secret。
- S3 object version id，若 provider 支持。
- S3 etag。
- S3 storage class，若可得。
- S3 archived at。
- local file mtime，若是本地存储。
- local file inode 不需要展示，避免暴露过多系统细节。
- remote URL 只显示 redacted host/path 摘要，不展示敏感 query。

状态信息：

- available / missing / deleted。
- deleted at。
- archived at。
- last storage error。
- last archive error。
- last download error。
- last integrity check at。
- integrity status：unchecked / ok / failed。
- 是否可删除。
- 是否可下载。
- 是否可归档到 S3。
- 是否私密。
- 加入私密时间。

操作：

- 下载。
- 删除。
- 归档到 S3。
- 设为私密 / 移出私密。
- 用于图生图。
- 打开 job。
- 复制 asset id。
- 复制 prompt。
- 复制 S3 key。

### 7.3 元数据展示优先级

Inspector 不应一次性展开所有字段。建议默认显示三组高频信息，其余放到折叠详情：

- 默认展开：基础信息、文件信息、存储位置。
- 默认折叠：完整 prompt / revised prompt、S3 细节、完整使用记录、完整错误。
- 调试展开：checksum、EXIF、color profile、perceptual hash、integrity check。

列表中每个技术字段都要支持复制，但复制按钮只在 hover 或 focus 时出现，避免常驻噪音。

## 8. 上传、去重与复用

用户上传的参考图和 Library 手动上传图都应进入图片库。它们不是生成输出，但同样是 Images 工作流中的图片资产。

### 8.1 Generate 参考图入库

当 Owner 在 `Generate` 中上传参考图时：

1. 后端先完成 MIME、大小、slot 数量和模式校验。
2. 后端为每张上传图创建 `image_assets` 记录，`asset_type = 'source_upload'`。
3. 后端按当前 storage backend 保存原图 bytes。默认保存本地；如果 S3 已启用，则优先保存到 S3。
4. 后端在 `image_generation_sources` 中记录 source slot，并引用 `asset_id`。
5. 后端继续把图片内容转成 provider 所需 payload，例如 xAI `/images/edits` 的 `image` / `images` 字段。
6. job 成功或失败都保留上传图资产和 source 引用，便于复查输入。

如果 job 创建校验失败，不应保存上传图资产，避免无效输入污染图片库。

### 8.2 Library 手动上传

Owner 可以在 `Library` 中直接上传图片，用于把已有图片纳入 Images 资产管理。

行为要求：

1. 后端必须完成 MIME、大小、文件名 basename 和图片内容校验。
2. 上传前计算图片内容 checksum，优先查找未删除、非私密的公开 Library 资产。
3. 如果 checksum 命中已有公开资产，直接返回已有 asset，并在响应中标记 duplicate；不得再次上传到 S3 或写入本地文件。
4. 如果未命中，则创建 `asset_type = 'manual_upload'` 的 image asset，并按当前 storage backend 保存 bytes。
5. S3 enabled 时仍遵循对象存储优先策略；但去重命中优先于任何新写入，避免重复占用对象存储。
6. 手动上传、去重命中和失败摘要应写入 audit；payload 只记录 asset id、是否 duplicate、storage、bytes 等摘要，不记录完整文件路径或敏感 URL。

MVP 中手动上传默认进入普通图片库，不直接创建私密资产。Owner 可以上传后再通过 `设为私密` 移入私密收藏夹。

### 8.3 去重语义

去重使用图片内容 checksum，目标是避免相同二进制图片重复写入本地磁盘或 S3。

适用范围：

- Library 手动上传。
- Generate 中上传的参考图。
- xAI 生成结果落库前的图片 bytes。

隐私边界：

- 普通上传和生成结果只复用未删除、非私密的公开资产。
- 私密收藏夹中的资产不参与普通去重复用，避免通过 duplicate 结果或普通 Library 视图暴露私密图片存在性。
- 后续如果需要私密范围内去重，应只在私密收藏夹已解锁的上下文中执行，并且响应不能泄露未授权资产信息。

去重只覆盖完全相同的图片 bytes。近似重复、缩放后相似图、不同编码但视觉相同的图片属于 perceptual hash 后续能力，不在 MVP 中实现。

### 8.4 上传图展示

Library 中上传图与生成图同列展示，但必须有低噪音类型标识：

- `Generated`：模型输出。
- `Uploaded`：用户上传参考图。
- `Manual upload`：Library 手动上传图。

上传图 tile 默认不显示 prompt；选中后 inspector 显示其关联 job、source slot、原始文件名摘要、MIME、大小、存储位置和引用关系。

### 8.5 Library 图片作为图生图参考

Library 中的图片可以快捷用于 `Generate` 的图生图场景。

交互要求：

- 图片 tile、查看器或 inspector 可提供 `用于图生图` 操作。
- 触发后切换到 `Generate`，自动选择 `image_to_image`，并把该 asset 填入第一个参考图槽位。
- 用户仍需填写 prompt 并显式提交生成任务；快捷操作本身不自动调用 provider。
- 已选择的 Library 图片应在参考图槽位中显示缩略图、名称和清除操作。

后端语义：

- 前端只提交 asset id，不提交本地文件路径。
- 后端必须校验 owner session；如果 asset 是私密图片，必须先校验当前 session 已解锁私密收藏夹。
- 后端读取 asset bytes 后转换为 provider 所需 payload，例如 data URL 或 multipart 内容。
- 不允许把需要 Phantom Lancer 登录态的 `/api/images/library/assets/<id>/content` URL 直接传给外部 provider。
- generation source 应记录该 asset id，便于 History 恢复输入引用。

### 8.6 URL 参考图

如果用户使用 URL 作为参考图，MVP 可以只记录 redacted URL，不默认把远程图下载入库。原因是 URL 可能是临时链接、带签名 query 或用户不希望持久化的远程资源。

后续可提供 `保存 URL 参考图副本` 开关：

- 开启后，后端下载 URL 内容并创建 `asset_type = 'source_url_snapshot'`。
- 下载必须遵守图片大小、MIME、timeout 和 redaction 规则。
- inspector 显示 `source URL snapshot` 状态和 redacted origin。

### 8.7 删除上传图

删除上传图只删除对应 image asset，不删除 job 历史。

- History 中仍显示 source slot 和文件摘要。
- 如果对应 job 依赖这张图，inspector 应提示“该图片曾作为参考图使用”。
- 删除后不能重放完全相同的 provider payload，除非 job 历史仍保留了原始 provider payload；MVP 不应保留完整 base64 payload。

## 9. 对象存储设计

### 9.1 存储模式

Images 支持两种持久化存储模式：

- `local`：默认模式。图片保存到 `<PL_DATA_DIR>/images/generated/`。
- `s3`：S3 API 兼容对象存储模式。图片优先保存到阿里云 OSS、腾讯云 COS、MinIO、Cloudflare R2 或其他兼容 S3 API 的对象存储。

这里的 `s3` 表示协议和 SDK 兼容层，不表示必须使用 AWS S3 服务。实现不能依赖 AWS 专有能力；除非用户选择真实 AWS S3，否则都应通过用户配置的 endpoint 访问对象存储。

当设置为 `s3` 后，生成完成时的保存顺序：

1. 后端从 xAI 结果获得图片 bytes。来源可以是 `b64_json`，也可以是远程 URL 下载后的 bytes。
2. 后端通过 S3 SDK 上传对象。
3. 上传成功后，DB 记录 `storage_backend = 's3'`、bucket、object key、etag/checksum、size、mime。
4. 后端不保留完整本地原图，除非用户显式启用 local fallback/cache。
5. 上传失败时按策略处理：默认 fallback 到 local，并记录 `images.asset.s3_upload_failed`。

默认建议：

- S3 enabled 时保留 `fallback_to_local = true`，避免上游已生成但对象存储临时失败导致图片完全丢失。
- S3 上传成功后不保存完整本地副本，降低本机磁盘占用。
- 后续可增加小尺寸缩略图本地缓存，但缓存必须有独立上限和清理策略。
- 用户上传参考图也遵循当前 storage backend；S3 enabled 时优先保存到 S3。

### 9.2 S3 SDK

后端使用 S3 API 兼容 SDK。Go 实现可以使用 AWS SDK for Go v2 的 S3 client，并配置 custom endpoint resolver；也可以后续替换为其他兼容 S3 API 的客户端。产品语义上只要求兼容 S3 API，不绑定 AWS 账号、AWS region 或 AWS bucket policy。

配置字段：

- storage backend：`local` / `s3`。
- provider label，例如 `aliyun-oss`、`tencent-cos`、`minio`、`r2`、`custom-s3`，仅用于 UI 展示和排障，不参与权限判断。
- bucket。
- region。部分兼容服务可能要求填写固定 region；后端只透传给 SDK，不按 AWS region 语义做强假设。
- endpoint。非 AWS 兼容对象存储必须配置 endpoint；真实 AWS S3 才允许为空并使用 SDK 默认 endpoint。
- object prefix，例如 `phantom-lancer/images/`。
- force path style，用于 MinIO、R2、部分兼容服务，具体按 provider 要求配置。
- access key id。
- secret access key。
- session token，可选。
- presign TTL，默认建议 10 分钟。
- fallback to local，默认 true。
- private bucket access mode：`proxy` / `presigned`，默认 `proxy`。

密钥处理：

- access key id 可以 masked 展示。
- secret access key 和 session token 只写入，不回显。
- audit 不记录明文 key，不记录完整 token。
- 错误消息需要 redacted，不能泄露 key、token、signed URL query。

### 9.3 Object Key 规则

object key 由后端生成，不包含 prompt、文件原名或用户输入。

建议格式：

```text
<prefix>/<asset_type>/<yyyy>/<mm>/<job_id>/<asset_id>-<slot>.<ext>
```

示例：

```text
phantom-lancer/images/generated/2026/06/imgjob_abc123/imgout_def456-01.png
phantom-lancer/images/source-upload/2026/06/imgjob_abc123/imgasset_ghi789-01.png
```

要求：

- key 只由受控字段拼接。
- extension 从 MIME type 推导。
- 上传时设置 `Content-Type`。
- 下载时设置 `Content-Disposition`。
- S3 object metadata 只放低敏字段，例如 asset id、job id、provider、model、checksum；不要放完整 prompt。
- 不依赖对象存储 provider 的公开 URL 规则；所有可访问 URL 都由 Phantom Lancer 后端 API 或短 TTL presign 生成。

### 9.4 私有 bucket 读取与下载

S3 bucket 不需要公开读。默认设计是 bucket 完全私有，所有读取都经过 Phantom Lancer 后端授权。

可选策略：

- 后端代理模式：浏览器请求 `/api/images/library/assets/{id}/content`，后端校验 owner session 后调用 S3 `GetObject` 并流式返回。
- 私有 presigned 模式：后端校验 owner session 后生成短 TTL presigned GET URL，前端直接加载该临时 URL。

MVP 默认使用后端代理模式，原因：

- bucket 可以保持 private，无需 public read policy。
- 前端不接触 bucket、object key 的完整访问能力和 S3 凭证。
- 不依赖对象存储 CORS 配置。
- Phantom Lancer 的 session、CSRF、审计和下载文件名控制都保持一致。

presigned 模式作为可选优化：

- 只在用户明确启用 direct signed URL 时使用。
- TTL 默认 10 分钟，可配置但必须有上限。
- signed URL 不持久化，不写 audit，不放入长期前端状态。
- 如果某个 S3 兼容 provider 的 presign、签名版本、虚拟主机 bucket 风格或 CORS 表现不稳定，应回退到后端代理。
- 即使使用 presigned URL，bucket policy 也仍然保持私有；临时 URL 由后端签名授权。

### 9.5 读取与下载

图片库读取图片时有两种策略：

- 后端代理：`GET /api/images/library/assets/{id}/content`，后端校验 session 后读取本地文件或 S3 object 并流式返回。
- 短 TTL presigned URL：`POST /api/images/library/assets/{id}/view-url` 返回临时 URL，前端直接加载。

MVP 建议：

- 本地图片使用后端代理。
- S3 图片默认使用后端代理，保证私有 bucket 可用且不需要 CORS。
- 下载统一走后端 `download` 接口，后端设置 `Content-Disposition`。
- presigned URL 只作为后续性能优化，不作为私有 bucket 的必要条件。

### 9.6 本地资产归档到 S3

当图片状态是 `local` 且 S3 已配置时，Library 支持手动 `归档到 S3`。

交互要求：

- 单图操作：选中本地资产后，toolbar 和 inspector 显示 `归档到 S3`。
- 批量操作：后续支持多选本地资产批量归档。
- 操作前显示确认，说明归档成功后本地完整原图会被删除，保留 S3 版本；如果启用 local cache，则只保留缩略图缓存。
- 归档中显示 per-asset 进度和失败原因。
- 归档成功后 tile storage 标识从 `Local` 改为 `S3`，inspector 显示 archived at、bucket 和 object key。
- 归档失败时保持本地文件不变，记录 last archive error。

后端流程：

1. 校验 owner session、CSRF 和 `images.asset.archive` 能力。
2. 查询 asset，确认 `storage_backend = 'local'` 且文件存在。
3. 读取本地文件并计算 checksum。
4. 使用 S3 SDK `PutObject` 上传到按规则生成的 object key。
5. 上传后可做 HeadObject 或 checksum/etag 校验。
6. DB transaction 更新 asset 为 `storage_backend = 's3'`，记录 bucket、key、etag、archived_at。
7. DB 更新成功后删除本地完整原图。
8. 发布 `images.asset.archived.s3` 事件并写 audit。

失败处理：

- 上传失败：DB 不变，本地文件不删。
- 校验失败：删除刚上传的对象或标记 orphan cleanup，DB 不变。
- DB 更新失败：尽量删除刚上传对象；如果删除失败，记录补偿任务。
- 本地删除失败：asset 状态仍可标记为 S3，但 inspector 显示 local cleanup pending，后续后台清理。

### 9.7 删除

删除 S3 图片时：

1. 后端检查 owner session 和 CSRF。
2. 查询 asset 记录和当前 storage backend。
3. 调用 S3 `DeleteObject`。
4. 物理删除成功后标记 DB `deleted_at`。
5. 写 audit：asset id、job id、storage backend、bucket hash 或 masked bucket、object key hash，不记录 secret。

如果对象已经不存在：

- 可以视为物理删除成功，但 audit payload 标记 `objectMissing: true`。
- inspector 后续展示为 deleted，而不是 missing。

## 10. API 草案

所有接口都需要 owner session。写操作必须校验 CSRF。

| Method | Path | 用途 |
| --- | --- | --- |
| `GET` | `/api/images/library/assets` | 查询图片库资产，支持 cursor、limit、q、storage、mode、status |
| `GET` | `/api/images/library/private/status` | 查询当前 session 是否已解锁私密收藏夹 |
| `POST` | `/api/images/library/private/unlock` | 使用 owner 登录密码解锁私密收藏夹，失败受 backoff 限制 |
| `POST` | `/api/images/library/private/lock` | 主动锁定当前 session 的私密收藏夹 |
| `GET` | `/api/images/library/assets/{id}` | 查询单张图片资产详情 |
| `GET` | `/api/images/library/assets/{id}/content` | 受控读取图片内容，本地存储默认使用 |
| `POST` | `/api/images/library/assets/{id}/view-url` | 生成短 TTL 查看 URL，S3 默认使用 |
| `GET` | `/api/images/library/assets/{id}/download` | 下载图片，设置 Content-Disposition |
| `DELETE` | `/api/images/library/assets/{id}` | 删除图片资产 |
| `POST` | `/api/images/library/assets/{id}/archive-s3` | 将本地图片资产归档到 S3 |
| `POST` | `/api/images/library/assets/{id}/private` | 设置或取消图片私密收藏夹状态 |
| `POST` | `/api/images/library/assets/{id}/restore` | 后续可选：恢复软删除记录 |
| `GET` | `/api/images/storage-settings` | 获取 storage 设置，secret 只返回 masked 状态 |
| `PUT` | `/api/images/storage-settings` | 更新 local / S3 存储设置 |
| `POST` | `/api/images/storage-settings/test` | 测试 S3 连接和 bucket 写权限 |

`GET /api/images/library/assets` 响应建议：

```json
{
  "items": [
    {
      "id": "imgout_xxx",
      "assetType": "generated",
      "jobId": "imgjob_xxx",
      "slot": 1,
      "status": "available",
      "private": false,
      "storageBackend": "s3",
      "storageAccessMode": "proxy",
      "thumbnailUrl": "/api/images/library/assets/imgout_xxx/content",
      "viewUrl": "",
      "downloadUrl": "/api/images/library/assets/imgout_xxx/download",
      "mimeType": "image/png",
      "sizeBytes": 1240021,
      "width": 1024,
      "height": 1024,
      "provider": "xai",
      "model": "grok-imagine-image-quality",
      "mode": "text_to_image",
      "sourceRole": "output",
      "promptPreview": "A quiet workstation...",
      "revisedPromptPreview": "",
      "createdAt": "2026-06-05T10:20:30Z"
    }
  ],
  "nextCursor": ""
}
```

## 11. 数据模型建议

由于图片库需要同时展示生成输出图和用户上传参考图，建议引入统一资产表 `image_assets`。`image_generation_outputs` 和 `image_generation_sources` 保留 job 语义，并通过 `asset_id` 指向图片资产。

建议新增图片资产表：

```sql
CREATE TABLE IF NOT EXISTS image_assets (
  id TEXT PRIMARY KEY,
  asset_type TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'available',
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
  checksum_sha256 TEXT NOT NULL DEFAULT '',
  perceptual_hash TEXT NOT NULL DEFAULT '',
  local_name TEXT NOT NULL DEFAULT '',
  storage_backend TEXT NOT NULL DEFAULT 'local',
  s3_bucket TEXT NOT NULL DEFAULT '',
  s3_region TEXT NOT NULL DEFAULT '',
  s3_endpoint_label TEXT NOT NULL DEFAULT '',
  s3_key TEXT NOT NULL DEFAULT '',
  s3_etag TEXT NOT NULL DEFAULT '',
  s3_version_id TEXT NOT NULL DEFAULT '',
  private_at TEXT NOT NULL DEFAULT '',
  archived_at TEXT NOT NULL DEFAULT '',
  deleted_at TEXT NOT NULL DEFAULT '',
  deleted_reason TEXT NOT NULL DEFAULT '',
  last_error TEXT NOT NULL DEFAULT '',
  last_integrity_check_at TEXT NOT NULL DEFAULT '',
  integrity_status TEXT NOT NULL DEFAULT 'unchecked',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
```

`asset_type` 取值：

- `generated`：模型生成输出图。
- `source_upload`：用户上传参考图。
- `source_url_snapshot`：后续可选，URL 参考图的受控副本。

`source_role` 取值：

- `output`：生成输出。
- `input_reference`：输入参考图。

建议扩展现有关系表：

```sql
ALTER TABLE image_generation_outputs ADD COLUMN asset_id TEXT NOT NULL DEFAULT '';
ALTER TABLE image_generation_sources ADD COLUMN asset_id TEXT NOT NULL DEFAULT '';
```

索引建议：

```sql
CREATE INDEX IF NOT EXISTS idx_image_assets_created_at ON image_assets(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_image_assets_type_created ON image_assets(asset_type, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_image_assets_storage_created ON image_assets(storage_backend, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_image_assets_status_created ON image_assets(status, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_image_assets_private_created ON image_assets(private, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_image_assets_job ON image_assets(job_id, slot);
CREATE INDEX IF NOT EXISTS idx_image_assets_checksum ON image_assets(checksum_sha256);
```

建议新增 storage settings 表：

```sql
CREATE TABLE IF NOT EXISTS image_storage_settings (
  id TEXT PRIMARY KEY,
  backend TEXT NOT NULL DEFAULT 'local',
  s3_provider_label TEXT NOT NULL DEFAULT 'custom-s3',
  s3_bucket TEXT NOT NULL DEFAULT '',
  s3_region TEXT NOT NULL DEFAULT '',
  s3_endpoint TEXT NOT NULL DEFAULT '',
  s3_prefix TEXT NOT NULL DEFAULT 'phantom-lancer/images',
  s3_force_path_style INTEGER NOT NULL DEFAULT 0,
  s3_access_key_id TEXT NOT NULL DEFAULT '',
  s3_secret_access_key TEXT NOT NULL DEFAULT '',
  s3_session_token TEXT NOT NULL DEFAULT '',
  s3_presign_ttl_seconds INTEGER NOT NULL DEFAULT 600,
  s3_access_mode TEXT NOT NULL DEFAULT 'proxy',
  fallback_to_local INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
```

`s3_access_mode = 'proxy'` 表示私有 bucket 默认通过后端代理读取；`presigned` 表示后端生成短 TTL URL，仍不需要 bucket public read。

## 12. 事件与审计

事件建议：

- `images.asset.created`
- `images.asset.source_uploaded`
- `images.asset.stored.local`
- `images.asset.stored.s3`
- `images.asset.s3_upload_failed`
- `images.asset.archive_requested`
- `images.asset.archived.s3`
- `images.asset.archive_failed`
- `images.asset.delete_requested`
- `images.asset.deleted`
- `images.asset.delete_failed`
- `images.asset.downloaded`
- `images.asset.private.added`
- `images.asset.private.removed`
- `images.private.unlocked`
- `images.private.locked`
- `images.private.rate_limited`
- `images.private.backoff_started`
- `images.storage.settings.updated`
- `images.storage.tested`

审计要求：

- 删除必须 audit。
- S3 设置更新必须 audit。
- S3 连接测试可以 audit，记录成功/失败和 bucket/endpoint 摘要。
- 本地归档到 S3 必须 audit，记录 asset id、job id、storage backend 变化和 object key hash。
- 用户上传参考图入库可以写低风险事件；audit 只记录 size、MIME、slot、job id，不记录完整文件名中的敏感路径。
- 下载可以选择只记录失败或按设置记录全部下载；默认个人模式下不必为每次查看图片写 audit，避免噪音。
- audit payload 不包含 secret、signed URL、完整远程 URL query、完整 prompt base64。

## 13. 权限与安全

能力建议：

- `images.library.read`：查看图片库和元数据。
- `images.asset.read`：读取图片内容。
- `images.asset.download`：下载图片。
- `images.asset.delete`：删除图片。
- `images.asset.archive`：将本地图片资产归档到 S3。
- `images.storage.settings.write`：更新对象存储设置。
- `images.storage.test`：测试对象存储连接。

安全要求：

- 所有图片内容读取都必须校验 owner session。
- 私密图片的列表、单图详情、内容读取、下载、删除和归档必须额外校验当前 session 的私密解锁状态。
- 进入私密收藏夹必须重新输入 owner 登录密码，不能只依赖已经登录的普通 session。
- 私密解锁状态只保存在服务端 session 维度并设置短 TTL；前端不能持久化私密解锁 token。
- 私密解锁失败必须进入登录失败限流体系或独立 backoff，至少包含 IP 维度。
- 删除和设置变更必须校验 CSRF。
- 本地归档到 S3 必须校验 CSRF。
- 本地文件读取继续使用安全文件名和路径边界检查。
- S3 object key 不能来自前端原样输入。
- S3 secret 不出现在前端 response、audit、日志和错误消息中。
- signed URL TTL 必须短，默认 10 分钟。
- presigned URL 不持久化到 DB。
- 私有 bucket 默认使用后端代理读取，不要求 public read policy。
- 下载响应需要设置安全的 `Content-Type` 和 `Content-Disposition`。
- 删除不应接受任意 path、bucket 或 key；只能按 DB 中的 asset id 操作。

## 14. 前端实现约束

必须遵守 AGENTS.md 中 Quiet Agent Workbench / Quiet DevOps Control Plane 约束：

- `Library` 是多媒体下的二级 tab，不是一级导航。
- 页面主对象是图片/视频资源资产，右侧 inspector 是当前资源上下文，不是装饰区。
- 使用浅色中性底、细边框、小圆角、低对比 hover/selected 状态。
- 主色只做克制强调；红色只用于删除危险操作。
- 不使用大 hero、大插画、营销 CTA、渐变背景、玻璃拟态或 AI 紫蓝光。
- 不把 toolbar、grid、inspector 做成层层嵌套卡片。
- 技术值如 asset id、job id、S3 key、checksum 使用 monospace。
- 长 prompt、object key 和错误消息必须截断并支持复制，不能撑破布局。
- 图标使用项目已有统一图标体系；不要手绘临时 SVG，不用 emoji 承担状态表达。
- 移动端 inspector 改为 drawer 或折叠区，避免与资源网格重叠。

建议前端模块结构：

```text
web/src/images/components/LibraryPanel.tsx
web/src/images/components/ImageAssetGrid.tsx
web/src/images/components/ImageAssetViewer.tsx
web/src/images/components/ImageAssetInspector.tsx
web/src/images/components/ImageStorageSettings.tsx
web/src/images/libraryTypes.ts
web/src/images/libraryApi.ts
```

入口 `ImagesView.tsx` 只负责 tab 状态、数据装配和动作传递，不应继续堆叠资源库网格、查看器、删除确认和存储表单实现。

## 15. 分阶段落地

### Phase 1：本地图片库

- 新增 `Library` tab。
- 新增统一 `image_assets` 表。
- 生成输出图创建 `generated` asset。
- 用户上传参考图创建 `source_upload` asset 并在 Library 展示。
- 支持图片网格、选中状态、右侧 inspector。
- 支持放大查看器。
- 支持单图下载。
- 支持单图删除，DB 标记 deleted，删除本地文件。
- 补充 audit 和基础测试。

### Phase 2：S3 对象存储

- 新增 image storage settings。
- 接入 S3 API 兼容 SDK。Go 实现可以使用 AWS SDK for Go v2 S3 client + custom endpoint，但不能把功能绑定到真实 AWS S3。
- Settings 中支持 local / S3 切换和连接测试。
- generation job 完成后优先上传 S3。
- S3 私有 bucket 默认使用后端代理读取，不要求 public read。
- S3 presigned URL 作为可选优化。
- 删除 S3 object。
- S3 上传失败 fallback local 并写事件。
- 支持本地图片资产归档到 S3。

### Phase 2.5：私密收藏夹

- 图片资产增加 `private` 和 `private_at` 字段。
- Library 增加普通图片库 / 私密收藏夹视图切换。
- 支持输入 owner 登录密码解锁私密收藏夹。
- 支持主动锁定当前 session 的私密收藏夹。
- 支持图片加入或移出私密收藏夹。
- 私密图片的列表、单图详情、内容、下载、删除、归档和旧本地 asset URL 都必须校验私密解锁状态。
- 解锁失败加入 IP 维度 backoff，并写入 audit。

### Phase 3：管理增强

- 多选批量删除。
- 批量下载 zip。
- 本地缩略图缓存和清理策略。
- 旧本地图片迁移到 S3。
- URL 参考图保存为 `source_url_snapshot`。
- 收藏、标签、集合。
- prompt / revised prompt 快速复制和重用。
- 重试保存到对象存储。

## 16. 验收标准

- Owner 能在 Images Library 看到之前每次成功生成的图片和用户上传过的参考图。
- 点击任意图片能放大查看。
- 右侧 inspector 能看到 asset type、job、slot、prompt、model、storage backend、MIME、大小、尺寸、checksum、创建时间等元数据。
- 图片可以下载，下载不暴露本地绝对路径或 S3 secret。
- 图片可以删除，删除需要确认，删除后 job 历史仍可查看。
- 图片可以加入私密收藏夹；加入后普通图片库默认不再展示该图。
- 私密收藏夹未解锁时不展示图片列表、缩略图或元数据；输入 owner 登录密码后才能进入。
- 私密图片的读取、下载、删除、归档和移出私密收藏夹都需要已解锁状态。
- Owner 可以主动锁定私密收藏夹，锁定后再次进入需要重新输入密码。
- 删除本地图片会删除对应文件；删除 S3 图片会调用 `DeleteObject`。
- 默认未配置对象存储时继续保存到本地。
- 配置 S3 后，生成结果优先保存到 S3，成功后不保留完整本地原图。
- 配置 S3 后，用户上传参考图也优先保存到 S3 并进入 Library。
- 本地图片资产支持归档到 S3；归档成功后本地完整原图被清理，归档失败时本地文件保持可用。
- S3 bucket 不需要公开读；默认通过后端代理读取和下载。
- S3 上传失败时按 fallback 策略保存到本地，并在 UI 和 audit 中可见。
- S3 secret、signed URL query、API Key 不进入前端持久状态、audit 明文或日志明文。
- 桌面和移动端布局不出现 toolbar、网格、inspector 或查看器重叠。
