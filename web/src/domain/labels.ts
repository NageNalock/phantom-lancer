import type { AuditEvent, CodexGatewaySettings, CodexGatewayStatus, CodexStatus, ImageProviderSettings, ImageStatus, ImageStorageSettings, StockAgentTraceSummary, StockDataHealth, StockSettings, StockSummary, StockV2DailyBarsQuality, StockV2DailyBarJob, StockV2Payload, StockV2Settings, StockV2UpdateJob, V2RaySettings, V2RayStatus } from "../app/types";

export const NAV_ITEMS = [
  { id: "dashboard", label: "控制台", description: "服务器状态、执行边界和下一步入口" },
  { id: "codex", label: "Codex", description: "本机 codex CLI 会话、工作区、审批和运行诊断" },
  { id: "codex-gateway", label: "Codex Gateway", description: "Codex OAuth 账号、OpenAI 兼容端点和请求审计" },
  { id: "logs", label: "日志", description: "服务日志、运行事件和在线排障视图" },
  { id: "images", label: "多媒体", description: "图片生成、视频生成、多图编辑、关键帧、资源库、历史和存储设置" },
  { id: "docker", label: "Docker", description: "Docker 守护进程、镜像与容器生命周期、daemon 安装与控制、内嵌 Registry" },
  { id: "stock", label: "股票V1", description: "账户/仓位、数据资产、人工策略、系统盯盘、Review 和操作确认闭环" },
  { id: "stockv2", label: "股票V2", description: "新一代股票系统：主数据管理、多组合仓位、智能更新与进度跟踪" },
  { id: "v2ray", label: "V2Ray", description: "内嵌 V2Ray 服务端、远程设备接入和运行控制" },
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
  "stock.portfolio.updated": "更新股票账户",
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

export function defaultStockSettings(): Required<StockSettings> {
  return {
    id: "default",
    proxyEnabled: false,
    proxyType: "http",
    proxyAddress: "",
    proxyUseForEastmoney: false,
    proxyUseForSina: false,
    proxyUseForTencent: false,
    quoteTtlSeconds: 60,
    autoRefreshEnabled: true,
    refreshIntervalSecs: 14400,
    defaultDataSource: "eastmoney",
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

// ==================== 股票 V2 标签 ====================

export function stockV2StatusLabel(payload?: StockV2Payload): string {
  if (payload?.updateJobs?.some(j => j.status === "running")) return "更新中";
  if (!payload?.portfolios?.length && !payload?.instruments?.length) return "未初始化";
  return "可用";
}

export function stockV2PortfolioCountLabel(payload?: StockV2Payload): string {
  if (!payload?.portfolios?.length) return "无组合";
  return `${payload.portfolios.length} 个组合`;
}

export function stockV2UpdateStatusLabel(job?: StockV2UpdateJob): string {
  if (!job) return "无记录";
  switch (job.status) {
    case "running": return "进行中";
    case "completed": return "已完成";
    case "failed": return "失败";
    case "cancelled": return "已取消";
    default: return job.status;
  }
}

export function stockV2UpdateStatusTone(job?: StockV2UpdateJob): "good" | "warn" | "danger" | "neutral" {
  if (!job) return "neutral";
  switch (job.status) {
    case "completed": return "good";
    case "running": return "warn";
    case "failed": return "danger";
    case "cancelled": return "neutral";
    default: return "neutral";
  }
}

export function stockV2TriggerTypeLabel(triggerType?: string): string {
  if (triggerType === "manual") return "手动更新";
  if (triggerType === "scheduled") return "定时更新";
  return triggerType || "未知";
}

export function stockV2RiskLabel(riskLevel?: string): string {
  if (riskLevel === "low") return "保守";
  if (riskLevel === "medium") return "均衡";
  if (riskLevel === "high") return "激进";
  return riskLevel || "未设置";
}

export function stockV2ValuationStatusLabel(status?: string): string {
  switch (status) {
    case "fresh": return "fresh";
    case "stale": return "stale";
    case "estimated": return "estimated";
    case "failed": return "failed";
    default: return status || "unknown";
  }
}

export function stockV2ValuationStatusTone(status?: string): "good" | "warn" | "danger" | "neutral" {
  switch (status) {
    case "fresh": return "good";
    case "stale": return "warn";
    case "estimated": return "warn";
    case "failed": return "danger";
    default: return "neutral";
  }
}

export function stockV2SettingsSummary(settings?: StockV2Settings): string {
  if (!settings) return "未配置";
  const parts: string[] = [];
  if (settings.autoUpdateEnabled) {
    const hours = Math.round(settings.updateIntervalSec / 3600 * 10) / 10;
    parts.push(`主数据 ${hours}h`);
  } else {
    parts.push("主数据手动");
  }
  parts.push(settings.dailyBarsAutoEnabled ? "日K自动" : "日K手动");
  return parts.join(" · ");
}

// ========== Daily Bars ==========

export function stockV2DailyBarsQualityLabel(q?: StockV2DailyBarsQuality): string {
  if (!q) return "未评估";
  if (!q.hasData) return "无数据";
  if (q.rowCount <= 0) return "空";
  if (q.stale) return `陈旧 · ${q.latestDate || "?"}`;
  if (!q.meets250) return `部分覆盖 · ${q.rowCount}根`;
  return `正常 · ${q.rowCount}根`;
}

export function stockV2DailyBarsQualityTone(q?: StockV2DailyBarsQuality): "good" | "warn" | "danger" | "neutral" {
  if (!q) return "neutral";
  if (!q.hasData || q.rowCount <= 0) return "danger";
  if (q.lastErrorMessage) return "danger";
  if (q.stale) return "warn";
  if (!q.meets250) return "warn";
  return "good";
}

export function stockV2DailyBarJobStatusLabel(j?: { status?: string }): string {
  if (!j?.status) return "无";
  switch (j.status) {
    case "running": return "进行中";
    case "completed": return "已完成";
    case "failed": return "失败";
    case "cancelled": return "已取消";
    default: return j.status;
  }
}

export function stockV2DailyBarJobStatusTone(j?: { status?: string }): "good" | "warn" | "danger" | "neutral" {
  if (!j?.status) return "neutral";
  switch (j.status) {
    case "completed": return "good";
    case "running": return "warn";
    case "failed": return "danger";
    case "cancelled": return "neutral";
    default: return "neutral";
  }
}

export function stockV2DailyBarJobTypeLabel(j?: { jobType?: string; mode?: string }): string {
  if (!j) return "";
  switch (j.jobType) {
    case "daily_bars_ensure": {
      if (j.mode === "symbol") return "单只补拉";
      return "按需补拉";
    }
    case "daily_bars_incremental": {
      if (j.mode === "hot") return "热集合增量";
      if (j.mode === "universe_incremental") return "全市场增量";
      return "增量";
    }
    default: return j.jobType || "日K任务";
  }
}

export function stockV2AdjustedLabel(adjusted?: string): string {
  switch (adjusted) {
    case "none": return "不复权";
    case "qfq": return "前复权";
    case "hfq": return "后复权";
    default: return adjusted || "不复权";
  }
}

export function stockV2RangeLabel(range?: string): string {
  switch (range) {
    case "6m": return "6 个月";
    case "1y": return "1 年";
    case "3y": return "3 年";
    case "5y": return "5 年";
    default: return range || "1 年";
  }
}
