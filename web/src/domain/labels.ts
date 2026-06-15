import type { AuditEvent, CodexGatewaySettings, CodexGatewayStatus, CodexStatus, ImageProviderSettings, ImageStatus, ImageStorageSettings, StockAgentTraceSummary, StockDataHealth, StockSummary, V2RaySettings, V2RayStatus } from "../app/types";

export const NAV_ITEMS = [
  { id: "dashboard", label: "控制台", description: "服务器状态、执行边界和下一步入口" },
  { id: "codex", label: "Codex", description: "本机 codex CLI 会话、工作区、审批和运行诊断" },
  { id: "codex-gateway", label: "Codex Gateway", description: "Codex OAuth 账号、OpenAI 兼容端点和请求审计" },
  { id: "logs", label: "日志", description: "服务日志、运行事件和在线排障视图" },
  { id: "images", label: "多媒体", description: "图片生成、视频生成、多图编辑、关键帧、资源库、历史和存储设置" },
  { id: "docker", label: "Docker", description: "Docker 守护进程、镜像与容器生命周期、daemon 安装与控制、内嵌 Registry" },
  { id: "stock", label: "股票", description: "账户/仓位、数据资产、人工策略、系统盯盘、Review 和操作确认闭环" },
  { id: "v2ray", label: "V2Ray", description: "内嵌 V2Ray 服务端、远程设备接入和运行控制" },
  { id: "mail", label: "Mail", description: "Mox 邮件服务控制面：域名、邮箱、证书、投递与搜索" },
  { id: "settings", label: "设置", description: "运行期配置、允许根目录和全局安全策略" },
] as const;

const auditLabels: Record<string, string> = {
  "owner.bootstrap": "初始化管理员",
  "auth.login": "登录",
  "codex_gateway.settings.updated": "更新 Codex Gateway 设置",
  "codex_gateway.api_key.created": "创建 Codex Gateway API key",
  "codex_gateway.api_key.rotated": "轮换 Codex Gateway API key",
  "codex_gateway.api_key.updated": "更新 Codex Gateway API key",
  "codex_gateway.api_key.deleted": "删除 Codex Gateway API key",
  "codex_gateway.account.created": "添加 Codex Gateway 账号",
  "codex_gateway.account.updated": "更新 Codex Gateway 账号",
  "codex_gateway.account.deleted": "删除 Codex Gateway 账号",
  "codex_gateway.account.refresh_requested": "刷新 Codex Gateway 账号",
  "codex_gateway.account.check_requested": "检查 Codex Gateway 账号",
  "codex_gateway.account.oauth_started": "开始 Codex Gateway OAuth",
  "codex_gateway.account.oauth_imported": "导入 Codex Gateway OAuth 账号",
  "codex_gateway.models.refresh_requested": "刷新 Codex Gateway 模型",
  "settings.update": "更新设置",
  "system.update.check": "检查系统更新",
  "system.update.start": "开始系统更新",
  "system.update.install": "安装系统更新",
  "system.update.restart_requested": "请求系统重启",
  "system.update.completed": "系统更新完成",
  "system.update.failed": "系统更新失败",
  "system.update.cancel": "取消系统更新",
  "system.update.confirm.rate_limited": "系统更新确认限流",
  "system.update.confirm.backoff_started": "系统更新确认退避",
  "images.settings.update": "更新多媒体设置",
  "images.storage.update": "更新多媒体存储",
  "images.storage.settings.updated": "更新多媒体存储设置",
  "images.storage.tested": "测试多媒体对象存储",
  "images.prompt.created": "创建生成预设",
  "images.prompt.updated": "更新生成预设",
  "images.prompt.deleted": "删除生成预设",
  "images.prompt.used": "带入生成预设",
  "images.job.completed": "多媒体任务完成",
  "images.job.failed": "多媒体任务失败",
  "images.asset.source_uploaded": "参考图已入库",
  "images.asset.stored.local": "资源已保存到本地",
  "images.asset.stored.s3": "资源已保存到对象存储",
  "images.asset.deleted": "资源已删除",
  "images.asset.archived.s3": "资源已归档到对象存储",
  "images.asset.store_failed": "资源保存失败",
  "images.asset.private.added": "加入多媒体私密收藏夹",
  "images.asset.private.removed": "移出多媒体私密收藏夹",
  "images.private.unlocked": "解锁多媒体私密收藏夹",
  "images.private.locked": "锁定多媒体私密收藏夹",
  "images.private.rate_limited": "多媒体私密解锁限流",
  "images.private.backoff_started": "多媒体私密解锁退避",
  "v2ray.settings.update": "更新 V2Ray 设置",
  "v2ray.config.validate": "校验 V2Ray 配置",
  "v2ray.service.start": "启动 V2Ray",
  "v2ray.service.stop": "停止 V2Ray",
  "v2ray.service.restart": "重启 V2Ray",
  "v2ray.client.create": "添加 V2Ray 远程设备",
  "v2ray.client.update": "更新 V2Ray 远程设备",
  "v2ray.client.rotate": "轮换 V2Ray UUID",
  "v2ray.client.revoke": "撤销 V2Ray 远程设备",
  "codex_cli.workspace.created": "登记 Codex 工作区",
  "codex_cli.workspace.updated": "更新 Codex 工作区",
  "codex_cli.thread.created": "创建 Codex 会话",
  "codex_cli.thread.archived": "归档 Codex 会话",
  "codex_cli.turn.started": "开始 Codex turn",
  "codex_cli.turn.interrupted": "中断 Codex turn",
  "codex_cli.app_server.start_requested": "请求启动 Codex app-server",
  "codex_cli.app_server.started": "Codex app-server 已启动",
  "codex_cli.app_server.start_failed": "Codex app-server 启动失败",
  "codex_cli.app_server.stopped": "Codex app-server 已停止",
  "codex_cli.approval.requested": "Codex 请求审批",
  "codex_cli.approval.approved": "允许 Codex 审批",
  "codex_cli.approval.denied": "拒绝 Codex 审批",
  "codex_cli.settings.updated": "更新 Codex 设置",
  "codex_cli.probe.failed": "Codex CLI 探测失败",
  "codex_cli.legacy_data.detected": "检测到旧版 Codex 数据",
  "stock.portfolio.created": "创建股票账户",
  "stock.portfolio.deleted": "删除股票账户",
  "stock.holding.saved": "保存股票持仓",
  "stock.quote.saved": "保存股票行情",
  "stock.data_source.saved": "保存股票数据源",
  "stock.data_source.checked": "检查股票数据源",
  "stock.instrument.refreshed": "刷新股票主数据",
  "stock.market_data.backfilled": "写入股票历史数据",
  "stock.news.ingested": "采集股票消息",
  "stock.strategy.created": "创建股票策略",
  "stock.opportunity.created": "创建股票机会",
  "stock.opportunity.strategy_created": "从股票机会生成策略",
  "stock.watch.created": "创建股票盯盘",
  "stock.watch.updated": "更新股票盯盘",
  "stock.watch.checked": "执行股票盯盘检查",
  "stock.alert.updated": "更新股票提醒",
  "stock.quote_refresh.checked": "检查股票行情刷新状态",
  "stock.review.created": "生成股票 Review",
  "stock.agent_profile.saved": "保存股票 Agent 模型配置",
  "stock.strategy_patch.accepted": "接受股票策略补丁",
  "stock.strategy_patch.rejected": "拒绝股票策略补丁",
  "stock.operation.confirmed": "确认股票操作",
  "stock.operation.cancelled": "作废股票操作建议",
  // Mail / Mox control plane (Phase 2+ will add more entries).
  "mail.binary.detected": "检测到 Mox 二进制",
  "mail.binary.downloaded": "下载 Mox 二进制",
  "mail.binary.installed": "安装 Mox 二进制",
  "mail.binary.uninstalled": "卸载 Mox 二进制",
  "mail.setup.initialized": "初始化 Mox 实例",
  "mail.setup.imported": "接入外部 Mox（只读模式）",
  "mail.setup.port_preflight.failed": "端口预检查失败",
  "mail.runtime.start_requested": "请求启动 Mox",
  "mail.runtime.started": "已启动 Mox",
  "mail.runtime.start_failed": "启动 Mox 失败",
  "mail.runtime.stop_requested": "请求停止 Mox",
  "mail.runtime.stopped": "已停止 Mox",
  "mail.runtime.restarted": "已重启 Mox",
  "mail.runtime.crashed": "Mox 异常退出",
  "mail.runtime.adopted": "接管孤儿 Mox 进程",
  "mail.runtime.probe_result": "Mox 探针结果",
  "mail.runtime.resolve_drift.accepted_overwrite": "配置漂移：以 Phantom 为准覆盖磁盘",
  "mail.runtime.resolve_drift.accepted_reimport": "配置漂移：以磁盘为准回导",
  "mail.config.apply.started": "Mail 配置应用开始",
  "mail.config.apply.succeeded": "Mail 配置应用成功",
  "mail.config.apply.rolled_back": "Mail 配置应用已回滚",
  "mail.config.apply.failed": "Mail 配置应用失败",
  "mail.domain.created": "Mail 创建域名",
  "mail.domain.updated": "Mail 更新域名",
  "mail.domain.deleted": "Mail 删除域名",
  "mail.domain.dns_check_requested": "Mail 请求 DNS 检查",
  "mail.account.created": "Mail 创建邮箱账户",
  "mail.account.updated": "Mail 更新邮箱账户",
  "mail.account.deleted": "Mail 删除邮箱账户",
  "mail.account.password_changed": "Mail 重置邮箱账户密码",
  "mail.alias.created": "Mail 创建别名",
  "mail.alias.updated": "Mail 更新别名",
  "mail.alias.deleted": "Mail 删除别名",
  "mail.certificate.issued": "Mail 签发证书",
  "mail.certificate.renewed": "Mail 续签证书",
  "mail.certificate.rotated": "Mail 替换证书",
  "mail.certificate.renewal_failed": "Mail 证书续签失败",
  "mail.queue.hold": "Mail 挂起投递队列项",
  "mail.queue.fail": "Mail 标记投递失败",
  "mail.queue.drop": "Mail 删除投递队列项",
  "mail.webhook.configured": "Mail 配置出站 Webhook",
  "mail.webhook.inbound_auth_failed": "Mail 入站 Webhook 鉴权失败",
  "mail.import.read_only.enabled": "Mail 启用只读导入模式",
  "mail.import.read_only.disabled": "Mail 禁用只读导入模式",
  "mail.backup.created": "Mail 创建配置备份",
  "mail.backup.deleted": "Mail 删除配置备份",
  "mail.settings.danger.wipe_data_requested": "Mail 请求危险操作：擦除 Mox 数据",
  "mail.retention.pruned": "Mail 保留策略清理旧数据",
  // Phase 5: Accounts / Aliases / Import Registration
  "mail.account.reset_password": "Mail 重置邮箱账户密码",
  "mail.account.resync_updated": "Mail 更新 IMAP 双向同步状态",
  "mail.import.registered": "Mail 接入外部实例注册",
  "mail.import.deleted": "Mail 移除外部实例接入",
  "mail.import.probed": "Mail 外部实例探针执行",
  // Phase 6: Delivery / Queue / Suppression / Webhook / Outbound
  "mail.delivery.listed": "Mail 查询投递事件列表",
  "mail.delivery.inspected": "Mail 查看投递事件详情",
  "mail.delivery.retried": "Mail 重试投递",
  "mail.delivery.deleted": "Mail 删除投递记录",
  "mail.delivery.pruned": "Mail 按保留期清理投递事件",
  "mail.queue.summarized": "Mail 查看队列摘要",
  "mail.queue.listed": "Mail 查询队列条目",
  "mail.queue.action.hold": "Mail 挂起队列条目",
  "mail.queue.action.unhold": "Mail 解除队列条目挂起",
  "mail.queue.action.schedule": "Mail 调度队列条目",
  "mail.queue.action.fail": "Mail 标记队列条目失败",
  "mail.queue.action.drop": "Mail 丢弃队列条目",
  "mail.suppression.listed": "Mail 查询抑制列表",
  "mail.suppression.created": "Mail 添加抑制条目",
  "mail.suppression.updated": "Mail 更新抑制条目",
  "mail.suppression.deleted": "Mail 删除抑制条目",
  "mail.suppression.imported": "Mail 批量导入抑制条目",
  "mail.suppression.pruned": "Mail 清理过期抑制条目",
  "mail.webhook.registered": "Mail 注册 Webhook",
  "mail.webhook.listed": "Mail 查看 Webhook 列表",
  "mail.webhook.deleted": "Mail 删除 Webhook 注册",
  "mail.webhook.secret_rotated": "Mail 轮换 Webhook 共享密钥",
  "mail.webhook.event.received": "Mail 收到 Webhook 事件",
  "mail.webhook.event.dropped": "Mail 丢弃 Webhook 事件（鉴权或限流）",
  "mail.outbound.rate.inspected": "Mail 查看出站速率窗口",
  "mail.outbound.thresholds.updated": "Mail 更新出站速率阈值",
  "mail.outbound.thresholds.exceeded": "Mail 出站速率超过阈值",
  "mail.reputation.dnsbl.probed": "Mail 执行 DNSBL 声誉探测",
  "mail.reputation.dnsbl.listed": "Mail DNSBL 检测到 IP 被列入黑名单",
  // ---- Phase 7: Mailbox / Folders / Messages / Search / Index / IMAP Sync / Compose ----
  "mail.folder.listed": "Mail 列举文件夹",
  "mail.folder.created": "Mail 创建文件夹",
  "mail.folder.updated": "Mail 更新文件夹",
  "mail.folder.deleted": "Mail 删除文件夹",
  "mail.message.listed": "Mail 列举邮件列表",
  "mail.message.viewed": "Mail 查看邮件详情",
  "mail.message.moved": "Mail 移动邮件到其他文件夹",
  "mail.message.flags_updated": "Mail 更新邮件标签/标记",
  "mail.message.deleted": "Mail 删除邮件",
  "mail.message.raw_fetched": "Mail 拉取邮件原文",
  "mail.search.executed": "Mail 执行全文搜索",
  "mail.index.health_viewed": "Mail 查看搜索索引健康",
  "mail.index.reset_requested": "Mail 请求重建搜索索引",
  "mail.imap_sync.started": "Mail 启动 IMAP 同步",
  "mail.imap_sync.paused": "Mail 暂停 IMAP 同步",
  "mail.imap_sync.resumed": "Mail 恢复 IMAP 同步",
  "mail.imap_sync.reset": "Mail 重置 IMAP 同步状态",
  "mail.compose.queued": "Mail 外发邮件已入队",
  "mail.draft.saved": "Mail 草稿已保存",
  "mail.draft.deleted": "Mail 草稿已删除",
  // ---- Phase 8: Logs / Backup / Retention / Danger Zone ----
  "mail.logs.tail_executed": "Mail 执行日志 Tail 读取",
  "mail.logs.stream_opened": "Mail 日志流式连接已开启",
  "mail.logs.stream_closed": "Mail 日志流式连接已关闭",
  "mail.logs.redaction_viewed": "Mail 查看日志脱敏规则摘要",
  "mail.backup.full_created": "Mail 创建完整数据备份",
  "mail.backup.schedule_created": "Mail 创建备份定时计划",
  "mail.backup.schedule_updated": "Mail 更新备份定时计划",
  "mail.backup.schedule_deleted": "Mail 删除备份定时计划",
  "mail.retention.rule_created": "Mail 创建保留策略规则",
  "mail.retention.rule_upserted": "Mail 更新保留策略规则",
  "mail.retention.rule_deleted": "Mail 删除保留策略规则",
  "mail.retention.manual_applied": "Mail 手动执行保留策略清理",
  "mail.danger.code_generated": "Mail 危险操作验证码已生成",
  "mail.danger.hard_delete_confirmed": "Mail 危险操作：硬删除确认通过",
  "mail.danger.hard_delete_completed": "Mail 危险操作：硬删除已执行",
};

export function auditLabel(type?: string): string {
  return auditLabels[type || ""] || type || "审计事件";
}

export function v2rayStateLabel(status?: V2RayStatus): string {
  if (!status) return "未知";
  if (status.running) return status.stale ? "运行中，配置待重启" : "运行中";
  if (status.state === "failed") return "失败";
  return status.enabled ? "已启用，未运行" : "已停止";
}

export function imageStatusLabel(status?: ImageStatus): string {
  if (!status) return "未知";
  if (!status.hasApiKey) return "未配置";
  if (status.lastJobStatus === "failed") return "最近失败";
  if (status.lastJobStatus === "queued" || status.lastJobStatus === "running") return "调用中";
  if (status.lastJobStatus === "interrupted") return "已中断";
  return "就绪";
}

export function codexGatewayStatusLabel(status?: CodexGatewayStatus): string {
  if (!status) return "未知";
  if (!status.enabled) return "未启用";
  if (!status.publicApiKeys) return "缺少 API key";
  if (!status.activeAccounts) return "缺少账号";
  if (status.recentFailureCount) return "最近失败";
  return "就绪";
}

export function stockStatusLabel(summary?: StockSummary): string {
  if (!summary?.portfolioCount) return "未建账户";
  if (summary.pendingOperationCount) return `${summary.pendingOperationCount} 待确认`;
  if (summary.openAlertCount) return `${summary.openAlertCount} 提醒`;
  if (summary.activeWatchCount) return "盯盘中";
  return "可用";
}

export function stockDataHealthLabel(health?: StockDataHealth): string {
  if (!health?.sourceCount && !health?.instrumentCount && !health?.newsItemCount && !health?.marketPointCount) return "未初始化";
  if (health.failedSources || health.failedTaskCount) return "存在失败";
  if (health.degradedSources || health.staleQuoteCount) return "部分降级";
  return "数据可用";
}

export function stockAgentTraceLabel(trace?: StockAgentTraceSummary): string {
  if (!trace?.runCount) return "未运行";
  if (trace.failedRunCount) return "存在失败";
  if (trace.pendingPatchCount) return `${trace.pendingPatchCount} 待确认`;
  return "可追溯";
}

export function codexGatewayAccountStatusLabel(value?: string): string {
  return (
    {
      active: "active",
      disabled: "disabled",
      invalid: "invalid",
      rate_limited: "rate limited",
    }[value || ""] ||
    value ||
    "unknown"
  );
}

export function imageJobStatusLabel(value?: string): string {
  return (
    {
      queued: "排队中",
      running: "运行中",
      success: "成功",
      failed: "失败",
      interrupted: "已中断",
    }[value || ""] ||
    value ||
    "未知"
  );
}

export function imageModeLabel(value?: string): string {
  return (
    {
      text_to_image: "文生图",
      image_to_image: "图生图",
      multi_image_edit: "多图编辑",
    }[value || ""] ||
    value ||
    "未知"
  );
}

export function mediaModeLabel(value?: string): string {
  return (
    {
      text_to_image: "文生图",
      image_to_image: "图生图",
      multi_image_edit: "多图编辑",
      text_to_video: "文生视频",
      image_to_video: "图生视频",
      multi_image_video: "多图生视频",
      keyframes: "关键帧视频",
    }[value || ""] ||
    value ||
    "未知"
  );
}

export function imageAssetTypeLabel(value?: string): string {
  return (
    {
      generated: "生成结果",
      source_upload: "用户上传",
      manual_upload: "手动上传",
      source_url: "URL 参考",
    }[value || ""] ||
    value ||
    "图片"
  );
}

export function imageStorageBackendLabel(value?: string): string {
  return (
    {
      local: "本地",
      s3: "S3 兼容对象存储",
      object_storage: "共享对象存储",
      remote: "远端 URL",
    }[value || ""] ||
    value ||
    "未知"
  );
}

export function v2rayTransportLabel(value?: string): string {
  return value === "ws" ? "WebSocket" : "TCP";
}

export function v2raySecurityLabel(value?: string): string {
  return value === "tls" ? "TLS" : "None";
}

export function formatDate(value?: string): string {
  if (!value) return "";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return new Intl.DateTimeFormat("zh-CN", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  }).format(date);
}

export function defaultRuntime() {
  return {
    allowedRoots: [],
    cookieSecure: false,
    tlsEnabled: false,
    tlsCertFile: "",
    tlsKeyFile: "",
    tlsOwnerUidCheck: true,
    hstsEnabled: false,
    hstsMaxAgeSeconds: 15724800,
    updatedAt: "",
  };
}

export function defaultV2RaySettings(): Required<V2RaySettings> {
  return {
    id: "default",
    enabled: false,
    startOnPhantomLaunch: false,
    assetDir: "",
    configMode: "guided",
    configFormat: "json",
    publicHost: "",
    listen: "0.0.0.0",
    port: 10086,
    protocol: "vmess",
    transport: "tcp",
    security: "none",
    wsPath: "/v2ray",
    tlsCertFile: "",
    tlsKeyFile: "",
    sniffingEnabled: true,
    blockPrivateNetwork: true,
    logLevel: "warning",
    rawConfigJson: "",
  };
}

export function defaultImageSettings(): Required<ImageProviderSettings> {
  return {
    id: "default",
    provider: "xai",
    hasApiKey: false,
    maskedApiKey: "",
    defaultModel: "grok-imagine-image-quality",
    defaultResponseFormat: "url",
    defaultResolution: "",
    defaultAspectRatio: "",
    historyRetention: 500,
    createdAt: "",
    updatedAt: "",
  };
}

export function defaultCodexGatewaySettings(): Required<CodexGatewaySettings> {
  return {
    id: "default",
    enabled: false,
    baseUrl: "https://chatgpt.com/backend-api",
    oauthAuthUrl: "https://auth.openai.com/oauth/authorize",
    oauthTokenUrl: "https://auth.openai.com/oauth/token",
    oauthClientId: "app_EMoamEEZ73f0CkXaXp7hrann",
    oauthRedirectUri: "http://localhost:1455/auth/callback",
    requestTimeoutSeconds: 600,
    refreshMarginSeconds: 300,
    accountHealthCheckIntervalSeconds: 43200,
    defaultInstructions: "You are a helpful assistant.",
    installationId: "",
    createdAt: "",
    updatedAt: "",
  };
}

export function defaultImageStorageSettings(): Required<ImageStorageSettings> {
  return {
    id: "default",
    backend: "local",
    objectStorageProfileId: "",
    s3ProviderLabel: "",
    s3Bucket: "",
    s3Region: "",
    s3Endpoint: "",
    s3Prefix: "phantom-lancer/images",
    s3ForcePathStyle: true,
    hasS3Credentials: false,
    maskedAccessKeyId: "",
    s3AccessMode: "proxy",
    fallbackToLocal: true,
    createdAt: "",
    updatedAt: "",
  };
}

export function auditSummary(event: AuditEvent): string {
  return event.summary || auditLabel(event.eventType);
}

export function maskSecret(value?: string): string {
  const text = value || "";
  if (!text) return "-";
  return text.length <= 12 ? "****" : `${text.slice(0, 8)}...${text.slice(-6)}`;
}

export function codexInstallStatusLabel(value?: string): string {
  return (
    {
      ready: "就绪",
      degraded: "降级（仅 exec/只读）",
      needs_setup: "需要配置",
      unavailable: "不可用",
    }[value || ""] ||
    value ||
    "未知"
  );
}

export function codexModuleStatusLabel(status?: CodexStatus): string {
  if (!status) return "未知";
  if (!status.enabled) return "未启用";
  const install = status.installation?.status;
  if (install === "needs_setup") return "需要配置";
  if (install === "unavailable") return "不可用";
  if (status.appServer?.state === "running") return "app-server 运行中";
  if (status.appServer?.state === "failed") return "app-server 失败";
  if (install === "degraded") return "降级（exec 兜底）";
  if (install === "ready") return "就绪（未启动）";
  return "就绪";
}

export function codexAppServerStateLabel(value?: string): string {
  return (
    {
      stopped: "已停止",
      starting: "启动中",
      running: "运行中",
      failed: "失败",
      degraded: "降级",
    }[value || ""] ||
    value ||
    "未知"
  );
}

export function codexThreadStatusLabel(value?: string): string {
  return (
    {
      idle: "空闲",
      running: "运行中",
      queued: "排队中",
      needs_approval: "待审批",
      failed: "失败",
      archived: "已归档",
    }[value || ""] ||
    value ||
    "未知"
  );
}

export function codexSandboxLabel(value?: string): string {
  return (
    {
      "read-only": "只读咨询",
      "workspace-write": "工作区写入",
    }[value || ""] ||
    value ||
    "只读咨询"
  );
}

export function codexEventTitle(type?: string): string {
  return (
    {
      "thread.started": "会话已开始",
      "thread.resumed": "会话已恢复",
      "thread.archived": "会话已归档",
      "turn.queued": "已排队",
      "turn.started": "开始处理",
      "turn.completed": "处理完成",
      "turn.failed": "处理失败",
      "turn.cancelled": "已中断",
      "message.user": "你",
      "message.agent": "Codex",
      "message.reasoning": "推理",
      "command.started": "执行命令",
      "command.completed": "命令完成",
      "command.owner.queued": "命令已排队",
      "command.owner.started": "Owner 命令",
      "command.owner.output": "命令输出",
      "command.owner.output.attached": "命令输出摘要",
      "command.owner.completed": "命令结束",
      "file_change.started": "开始修改文件",
      "file_change.completed": "文件修改完成",
      "approval.requested": "请求审批",
      "approval.resolved": "审批已处理",
      "tool.started": "调用工具",
      "tool.completed": "工具完成",
      "plan.updated": "计划更新",
      "diff.updated": "Diff 更新",
      "review.comment.created": "Review 评论",
      "browser.preview.opened": "预览已打开",
      "browser.preview.comment": "预览评论",
      "thread.status.changed": "会话状态变化",
      "usage.updated": "用量更新",
      "diagnostic.warning": "警告",
      "diagnostic.error": "错误",
    }[type || ""] ||
    type ||
    "事件"
  );
}
