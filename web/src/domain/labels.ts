import type { AuditEvent, CodexGatewaySettings, CodexGatewayStatus, CodexStatus, ImageProviderSettings, ImageStatus, ImageStorageSettings, StockV2DailyBarsQuality, StockV2Payload, StockV2Settings, StockV2UpdateJob, V2RaySettings, V2RayStatus } from "../app/types";

export const NAV_ITEMS = [
  { id: "dashboard", label: "控制台", description: "服务器状态、执行边界和下一步入口" },
  { id: "codex", label: "Codex", description: "本机 codex CLI 会话、工作区、审批和运行诊断" },
  { id: "codex-gateway", label: "Codex Gateway", description: "Codex OAuth 账号、OpenAI 兼容端点和请求审计" },
  { id: "logs", label: "日志", description: "服务日志、运行事件和在线排障视图" },
  { id: "images", label: "多媒体", description: "图片生成、视频生成、多图编辑、关键帧、资源库、历史和存储设置" },
  { id: "docker", label: "Docker", description: "Docker 守护进程、镜像与容器生命周期、daemon 安装与控制、内嵌 Registry" },
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

// ==================== 股票 V2 标签 ====================

export function stockV2StatusLabel(payload?: StockV2Payload): string {
  if (payload?.updateJobs?.some(j => j.status === "running")) return "更新中";
  if (!payload?.portfolios?.length && !(payload?.instrumentTotal || payload?.instruments?.length)) return "未初始化";
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

export function stockV2InstrumentTypeLabel(type?: string): string {
  switch (type) {
    case "exchange_fund": return "场内基金";
    case "stock":
    case "":
    case undefined: return "股票";
    default: return type;
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
    case "fresh": return "最新行情";
    case "stale": return "旧价沿用";
    case "estimated": return "成本估算";
    case "failed": return "无价格";
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
    parts.push("数据资产每日23点");
  } else {
    parts.push("数据资产手动");
  }
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

// ========== Strategy ==========

export function stockV2StrategyKindLabel(kind?: string): string {
  switch (kind) {
    case "symbol_strategy": return "单票策略";
    case "portfolio_monitor": return "组合监控";
    default: return kind || "策略";
  }
}

export function stockV2StrategyStatusLabel(status?: string): string {
  switch (status) {
    case "draft": return "草稿";
    case "active": return "生效中";
    case "paused": return "已暂停";
    case "archived": return "已归档";
    default: return status || "未知";
  }
}

export function stockV2StrategyStatusTone(status?: string): "good" | "warn" | "danger" | "neutral" {
  switch (status) {
    case "active": return "good";
    case "draft": return "neutral";
    case "paused": return "warn";
    case "archived": return "neutral";
    default: return "neutral";
  }
}

export function stockV2StrategyScopeLabel(scope?: string): string {
  switch (scope) {
    case "research": return "账户无关";
    case "portfolio_bound": return "绑定组合";
    default: return scope || "-";
  }
}

export function stockV2StrategySourceLabel(source?: string): string {
  switch (source) {
    case "manual": return "人工录入";
    case "system_template": return "系统模板";
    case "agent": return "Agent 生成";
    default: return source || "-";
  }
}

export function stockV2StrategyDirectionLabel(direction?: string): string {
  switch (direction) {
    case "watch": return "观察";
    case "bullish": return "偏多";
    case "bearish": return "偏空";
    case "neutral": return "中性";
    case "buy_signal": return "偏多";
    case "sell_signal": return "偏空";
    case "hold": return "中性持有";
    default: return direction || "-";
  }
}

export function stockV2StrategyVersionStatusLabel(status?: string): string {
  switch (status) {
    case "draft": return "草稿";
    case "active": return "当前版本";
    case "superseded": return "历史版本";
    default: return status || "未知";
  }
}

export function stockV2StrategyVersionStatusTone(status?: string): "good" | "warn" | "danger" | "neutral" {
  switch (status) {
    case "active": return "good";
    case "draft": return "warn";
    case "superseded": return "neutral";
    default: return "neutral";
  }
}

// ========== Watch / Alert ==========

export function stockV2WatchStatusLabel(status?: string): string {
  switch (status) {
    case "active": return "盯盘中";
    case "paused": return "已暂停";
    case "archived": return "已归档";
    default: return status || "未知";
  }
}

export function stockV2WatchStatusTone(status?: string): "good" | "warn" | "danger" | "neutral" {
  switch (status) {
    case "active": return "good";
    case "paused": return "warn";
    case "archived": return "neutral";
    default: return "neutral";
  }
}

export function stockV2WatchSourceLabel(source?: string): string {
  switch (source) {
    case "manual": return "人工创建";
    case "strategy": return "来自策略";
    case "portfolio_monitor": return "组合监控";
    default: return source || "-";
  }
}

export function stockV2WatchTriggerLabel(kind?: string): string {
  switch (kind) {
    case "price_above": return "价格突破";
    case "price_below": return "价格跌破";
    case "price_between": return "价格区间";
    case "pct_change_above": return "涨幅超限";
    case "pct_change_below": return "跌幅超限";
    case "quote_stale": return "行情过期";
    case "daily_close_above": return "日收盘突破";
    case "daily_close_below": return "日收盘跌破";
    case "portfolio_symbol_weight_above": return "组合权重过高";
    case "portfolio_symbol_weight_below": return "组合权重低于";
    // 旧前端草稿字段,保留展示兼容。
    case "pct_change_up": return "涨幅超限";
    case "pct_change_down": return "跌幅超限";
    case "data_stale": return "行情过期";
    case "portfolio_weight_high": return "组合权重过高";
    default: return kind || "-";
  }
}

/** 是否为百分比类阈值(用于规则摘要加 % 后缀)。 */
export function stockV2WatchTriggerIsPercent(kind?: string): boolean {
  return kind === "pct_change_above" ||
    kind === "pct_change_below" ||
    kind === "portfolio_symbol_weight_above" ||
    kind === "portfolio_symbol_weight_below" ||
    kind === "pct_change_up" ||
    kind === "pct_change_down" ||
    kind === "portfolio_weight_high";
}

export function stockV2WatchScheduleLabel(schedule?: string): string {
  switch (schedule) {
    case "manual": return "手动";
    case "market_session": return "盘中";
    case "daily": return "每日";
    // 旧前端草稿字段,保留展示兼容。
    case "continuous": return "持续";
    case "market_open": return "盘中";
    case "hourly": return "每小时";
    default: return schedule || "-";
  }
}

/** 规则摘要:后端 ruleSummary 优先,否则按 triggerKind + threshold 拼。 */
export function stockV2WatchRuleSummary(watch?: {
  ruleSummary?: string;
  triggerKind?: string;
  threshold?: number;
  triggerConfig?: { rules?: Array<{ type?: string; ruleType?: string; threshold?: number; low?: number; high?: number; maxAgeSeconds?: number }> };
} | null): string {
  if (!watch) return "-";
  if (watch.ruleSummary?.trim()) return watch.ruleSummary.trim();
  const rules = Array.isArray(watch.triggerConfig?.rules) ? watch.triggerConfig.rules : [];
  if (rules.length > 0) {
    const labels = rules.slice(0, 3).map((rule) => {
      const kind = rule.type || rule.ruleType || "";
      const label = stockV2WatchTriggerLabel(kind);
      if (kind === "price_between") return `${label} ${rule.low ?? "-"}~${rule.high ?? "-"}`;
      if (kind === "quote_stale") return `${label} ${Math.round((rule.maxAgeSeconds || 1800) / 60)}min`;
      if (typeof rule.threshold === "number" && Number.isFinite(rule.threshold)) {
        return `${label} ${rule.threshold}${stockV2WatchTriggerIsPercent(kind) ? "%" : ""}`;
      }
      return label;
    });
    return `${labels.join(" / ")}${rules.length > 3 ? ` 等 ${rules.length} 条` : ""}`;
  }
  const kind = stockV2WatchTriggerLabel(watch.triggerKind);
  if (typeof watch.threshold === "number" && Number.isFinite(watch.threshold)) {
    const suffix = stockV2WatchTriggerIsPercent(watch.triggerKind) ? "%" : "";
    return `${kind} ${watch.threshold}${suffix}`;
  }
  return kind;
}

export function stockV2WatchRunStatusTone(status: "matched" | "not_matched" | "skipped" | "degraded" | string): "good" | "warn" | "danger" | "neutral" {
  switch (status) {
    case "matched": return "warn";
    case "degraded": return "danger";
    case "skipped": return "neutral";
    case "not_matched": return "good";
    default: return "neutral";
  }
}

export function stockV2AlertStatusLabel(status?: string): string {
  switch (status) {
    case "open": return "待处理";
    case "acknowledged": return "已确认";
    case "ignored": return "已忽略";
    case "resolved": return "已解决";
    default: return status || "未知";
  }
}

export function stockV2AlertStatusTone(status?: string): "good" | "warn" | "danger" | "neutral" {
  switch (status) {
    case "open": return "warn";
    case "acknowledged": return "neutral";
    case "ignored": return "neutral";
    case "resolved": return "good";
    default: return "neutral";
  }
}

export function stockV2AlertLevelLabel(level?: string): string {
  switch (level) {
    case "info": return "提示";
    case "warning": return "警告";
    case "critical": return "紧急";
    // 旧前端草稿字段,保留展示兼容。
    case "warn": return "警告";
    case "danger": return "紧急";
    default: return level || "提示";
  }
}

export function stockV2AlertLevelTone(level?: string): "good" | "warn" | "danger" | "neutral" {
  switch (level) {
    case "info": return "neutral";
    case "warning": return "warn";
    case "critical": return "danger";
    case "warn": return "warn";
    case "danger": return "danger";
    default: return "neutral";
  }
}

// ========== Monitor(监控与任务)==========

export function stockV2MonitorTaskTypeLabel(taskType?: string): string {
  switch (taskType) {
    case "universe_update": return "旧数据资产维护";
    case "latest_quote_refresh": return "最新行情刷新";
    case "daily_bars_sync": return "旧日K抓取";
    case "data_strategy_monitor": return "数据面策略监控";
    case "portfolio_risk_monitor": return "组合风险监控";
    case "news_strategy_monitor": return "消息面策略监控";
    case "daily_fundamental_monitor": return "每日基本面监控";
    case "data_quality_monitor": return "数据质量监控";
    default: return taskType || "监控任务";
  }
}

export function stockV2MonitorCategoryLabel(category?: string): string {
  switch (category) {
    case "data": return "数据任务";
    case "strategy": return "策略监控";
    case "portfolio": return "组合监控";
    case "news": return "消息面";
    case "fundamental": return "基本面";
    case "quality": return "数据质量";
    default: return category || "-";
  }
}

export function stockV2MonitorRunStatusLabel(status?: string): string {
  switch (status) {
    case "running": return "运行中";
    case "completed": return "已完成";
    case "failed": return "失败";
    case "cancelled": return "已取消";
    default: return status || "未知";
  }
}

export function stockV2MonitorRunStatusTone(status?: string): "good" | "warn" | "danger" | "neutral" {
  switch (status) {
    case "completed": return "good";
    case "running": return "warn";
    case "failed": return "danger";
    case "cancelled": return "neutral";
    default: return "neutral";
  }
}

export function stockV2MonitorHitStatusLabel(status?: string): string {
  switch (status) {
    case "candidate": return "候选";
    case "doublechecked": return "已复核";
    case "alerted": return "已提醒";
    case "reviewed": return "已Review";
    case "ignored": return "已忽略";
    default: return status || "未知";
  }
}

export function stockV2MonitorHitStatusTone(status?: string): "good" | "warn" | "danger" | "neutral" {
  switch (status) {
    case "candidate": return "warn";
    case "doublechecked": return "neutral";
    case "alerted": return "danger";
    case "reviewed": return "good";
    case "ignored": return "neutral";
    default: return "neutral";
  }
}

export function stockV2MonitorAgentStateLabel(state?: string): string {
  switch (state) {
    case "not_enabled": return "未启用";
    case "pending": return "待调用";
    case "enabled_no_executor": return "已启用(未接执行器)";
    case "skipped": return "已跳过";
    default: return state || "-";
  }
}

// ========== 组合哨兵 (Portfolio Sentinel) ==========

export function stockV2SentinelWindowLabel(window?: string): string {
  switch (window) {
    case "manual": return "手动";
    case "pre_market": return "盘前";
    case "midday": return "午间";
    case "post_close": return "盘后/夜间";
    default: return window || "-";
  }
}

export function stockV2SentinelTriggerLabel(trigger?: string): string {
  switch (trigger) {
    case "manual": return "手动触发";
    case "scheduled": return "定时触发";
    default: return trigger || "-";
  }
}

export function stockV2SentinelStatusLabel(status?: string): string {
  switch (status) {
    case "running": return "运行中";
    case "completed": return "已完成";
    case "failed": return "失败";
    case "cancelled": return "已取消";
    case "pending": return "等待中";
    default: return status || "-";
  }
}

export function stockV2SentinelStatusTone(status?: string): "good" | "warn" | "danger" | "neutral" {
  switch (status) {
    case "completed": return "good";
    case "running":
    case "pending": return "warn";
    case "failed": return "danger";
    default: return "neutral";
  }
}

// 风险级别同时兼容两套取值:代码常量 low/medium/high/critical 与文档正文 none/info/warning/critical。
export function stockV2SentinelRiskLabel(level?: string): string {
  switch (level) {
    case "none": return "无风险";
    case "info": return "提示";
    case "low": return "低";
    case "warning": return "警告";
    case "medium": return "中";
    case "high": return "高";
    case "critical": return "紧急";
    default: return level || "-";
  }
}

export function stockV2SentinelRiskTone(level?: string): "good" | "warn" | "danger" | "neutral" {
  switch (level) {
    case "none":
    case "info":
    case "low": return "neutral";
    case "warning":
    case "medium": return "warn";
    case "high":
    case "critical": return "danger";
    default: return "neutral";
  }
}

// ===== Operation Review / Agent 治理层 label =====

export function stockV2ReviewStatusLabel(status?: string): string {
  switch (status) {
    case "pending": return "待处理";
    case "running": return "运行中";
    case "completed": return "已完成";
    case "failed": return "失败";
    case "closed": return "已关闭";
    default: return status || "未知";
  }
}

export function stockV2ReviewStatusTone(status?: string): "good" | "warn" | "danger" | "neutral" {
  switch (status) {
    case "completed": return "good";
    case "running": return "neutral";
    case "pending": return "warn";
    case "failed": return "danger";
    default: return "neutral";
  }
}

export function stockV2ReviewOutputTypeLabel(type?: string): string {
  switch (type) {
    case "trade_signal": return "交易信号";
    case "proposed_operation": return "操作提案";
    case "strategy_patch": return "策略补丁";
    case "ignore": return "忽略";
    case "continue_monitoring": return "继续监控";
    default: return type || "-";
  }
}

export function stockV2AgentProviderTypeLabel(type?: string): string {
  switch (type) {
    case "openai": return "OpenAI";
    case "codex_cli": return "Codex CLI";
    case "local": return "本地";
    default: return type || "-";
  }
}

export function stockV2AgentConfigStateLabel(state?: string): string {
  switch (state) {
    case "not_configured": return "未配置";
    case "configured": return "已配置";
    case "misconfigured": return "配置异常";
    default: return state || "-";
  }
}

export function stockV2AgentAuthStateLabel(state?: string): string {
  switch (state) {
    case "unauthenticated": return "未认证";
    case "authenticated": return "已认证";
    case "expired": return "已过期";
    case "unknown": return "未知";
    default: return state || "-";
  }
}

export function stockV2AgentAvailabilityLabel(state?: string): string {
  switch (state) {
    case "available": return "可用";
    case "unavailable": return "不可用";
    case "degraded": return "降级";
    case "unknown": return "未知";
    default: return state || "-";
  }
}

export function stockV2AgentAvailabilityTone(state?: string): "good" | "warn" | "danger" | "neutral" {
  switch (state) {
    case "available": return "good";
    case "degraded": return "warn";
    case "unavailable": return "danger";
    default: return "neutral";
  }
}

export function stockV2AgentModelStatusLabel(status?: string): string {
  switch (status) {
    case "available": return "可用";
    case "degraded": return "降级";
    case "unavailable": return "不可用";
    default: return status || "-";
  }
}

export function stockV2AgentModelStatusTone(status?: string): "good" | "warn" | "danger" | "neutral" {
  switch (status) {
    case "available": return "good";
    case "degraded": return "warn";
    case "unavailable": return "danger";
    default: return "neutral";
  }
}

export function stockV2AgentModelCostLevelLabel(level?: string): string {
  switch (level) {
    case "low": return "低成本";
    case "medium": return "中成本";
    case "high": return "高成本";
    default: return level || "-";
  }
}

export function stockV2AgentTaskTypeLabel(taskType?: string): string {
  switch (taskType) {
    case "operation_review": return "操作复核";
    case "strategy_generation": return "策略生成";
    case "opportunity_discovery": return "机会发现";
    case "news_event_review": return "消息面研判";
    case "portfolio_risk_review": return "组合风险审查";
    case "stock_profile_summary": return "股票画像生成";
    case "bull_bear_debate": return "多空辩论";
    default: return taskType || "-";
  }
}

export function stockV2AgentTaskConfigurable(taskType?: string): boolean {
  return taskType === "operation_review" || taskType === "strategy_generation" || taskType === "opportunity_discovery" || taskType === "stock_profile_summary";
}

export function stockV2AgentRunStatusLabel(status?: string): string {
  switch (status) {
    case "pending": return "待运行";
    case "ready": return "就绪";
    case "running": return "运行中";
    case "completed": return "已完成";
    case "failed": return "失败";
    default: return status || "未知";
  }
}

export function stockV2AgentRunStatusTone(status?: string): "good" | "warn" | "danger" | "neutral" {
  switch (status) {
    case "completed": return "good";
    case "running": return "neutral";
    case "ready": return "neutral";
    case "pending": return "warn";
    case "failed": return "danger";
    default: return "neutral";
  }
}

export function stockV2GuardrailsStatusLabel(status?: string): string {
  switch (status) {
    case "pass": return "通过";
    case "blocked": return "已拦截";
    case "degraded": return "降级";
    default: return status || "-";
  }
}

export function stockV2GuardrailsStatusTone(status?: string): "good" | "warn" | "danger" | "neutral" {
  switch (status) {
    case "pass": return "good";
    case "blocked": return "danger";
    case "degraded": return "warn";
    default: return "neutral";
  }
}

// ========== Opportunity Discovery (主题机会发现) ==========

export function stockV2OpportunityStatusLabel(status?: string): string {
  switch (status) {
    case "draft": return "草稿";
    case "researching": return "研究中";
    case "completed": return "已完成";
    case "closed": return "已关闭";
    default: return status || "未知";
  }
}

export function stockV2OpportunityStatusTone(status?: string): "good" | "warn" | "danger" | "neutral" {
  switch (status) {
    case "completed": return "good";
    case "researching": return "warn";
    case "draft": return "neutral";
    case "closed": return "neutral";
    default: return "neutral";
  }
}

export function stockV2OpportunityMarketScopeLabel(scope?: string): string {
  switch (scope) {
    case "a_share": return "A股";
    case "hk": return "港股";
    case "us": return "美股";
    case "all": return "全部";
    default: return scope || "-";
  }
}

export function stockV2OpportunityInstrumentScopeLabel(scope?: string): string {
  switch (scope) {
    case "stock": return "个股";
    case "exchange_fund": return "ETF";
    case "both": return "个股+ETF";
    default: return scope || "-";
  }
}

export function stockV2DiscoveryRunStatusLabel(status?: string): string {
  switch (status) {
    case "pending": return "待运行";
    case "running": return "运行中";
    case "completed": return "已完成";
    case "failed": return "失败";
    case "cancelled": return "已取消";
    default: return status || "未知";
  }
}

export function stockV2DiscoveryRunStatusTone(status?: string): "good" | "warn" | "danger" | "neutral" {
  switch (status) {
    case "completed": return "good";
    case "running": return "neutral";
    case "pending": return "warn";
    case "failed": return "danger";
    case "cancelled": return "warn";
    default: return "neutral";
  }
}

// 固定 8 步中文标题，对齐设计文档 §4.3
export function stockV2DiscoveryStepLabel(key?: string): string {
  switch (key) {
    case "understand_theme": return "主题理解";
    case "internal_recall": return "项目内资料召回";
    case "external_research": return "外部公开资料搜索";
    case "theme_chain": return "产业链拆解";
    case "candidate_merge": return "候选合并与去噪";
    case "market_risk_check": return "行情与风险检查";
    case "candidate_ranking": return "候选排序";
    case "final_report": return "最终报告";
    default: return key || "-";
  }
}

export function stockV2DiscoveryStepStatusLabel(status?: string): string {
  switch (status) {
    case "pending": return "待执行";
    case "running": return "执行中";
    case "completed": return "已完成";
    case "failed": return "失败";
    default: return status || "未知";
  }
}

export function stockV2DiscoveryStepStatusTone(status?: string): "good" | "warn" | "danger" | "neutral" {
  switch (status) {
    case "completed": return "good";
    case "running": return "warn";
    case "pending": return "neutral";
    case "failed": return "danger";
    default: return "neutral";
  }
}

export function stockV2CandidateRelationTypeLabel(type?: string): string {
  switch (type) {
    case "direct": return "直接相关";
    case "supply_chain": return "供应链";
    case "theme_etf": return "主题ETF";
    case "competitor": return "竞争";
    case "weak": return "弱相关";
    default: return type || "-";
  }
}

export function stockV2CandidateRelationTypeTone(type?: string): "good" | "warn" | "danger" | "neutral" {
  switch (type) {
    case "direct": return "good";
    case "supply_chain": return "good";
    case "theme_etf": return "neutral";
    case "competitor": return "warn";
    case "weak": return "neutral";
    default: return "neutral";
  }
}

export function stockV2CandidateStatusLabel(status?: string): string {
  switch (status) {
    case "candidate": return "候选";
    case "shortlisted": return "已入围";
    case "rejected": return "已排除";
    case "strategy_requested": return "策略生成中";
    case "strategy_generated": return "已生成策略";
    default: return status || "未知";
  }
}

export function stockV2CandidateStatusTone(status?: string): "good" | "warn" | "danger" | "neutral" {
  switch (status) {
    case "shortlisted": return "good";
    case "strategy_generated": return "good";
    case "strategy_requested": return "warn";
    case "candidate": return "neutral";
    case "rejected": return "danger";
    default: return "neutral";
  }
}

export function stockV2EvidenceSourceTypeLabel(type?: string): string {
  switch (type) {
    case "internal_profile": return "内部画像";
    case "internal_news": return "内部新闻";
    case "quote": return "行情";
    case "daily_bar": return "日K";
    case "external_source": return "外部来源";
    case "agent_note": return "Agent备注";
    case "semantic_recall": return "语义召回";
    default: return type || "-";
  }
}

export function stockV2EvidenceSourceTypeTone(type?: string): "good" | "warn" | "danger" | "neutral" {
  switch (type) {
    case "semantic_recall": return "neutral";
    case "internal_profile": return "good";
    case "internal_news": return "good";
    case "quote": return "neutral";
    case "daily_bar": return "neutral";
    case "external_source": return "warn";
    case "agent_note": return "neutral";
    default: return "neutral";
  }
}

export function stockV2EmbeddingAssetStatusLabel(status?: string): string {
  switch (status) {
    case "ready": return "就绪";
    case "stale": return "过期";
    case "failed": return "失败";
    default: return status || "-";
  }
}

export function stockV2EmbeddingAssetStatusTone(status?: string): "good" | "warn" | "danger" | "neutral" {
  switch (status) {
    case "ready": return "good";
    case "stale": return "warn";
    case "failed": return "danger";
    default: return "neutral";
  }
}

export function stockV2EmbeddingErrorCodeLabel(code?: string): string {
  switch (code) {
    case "embedding_model_not_configured": return "未绑定嵌入模型";
    case "embedding_model_unavailable": return "嵌入模型不可用";
    case "embedding_asset_not_ready": return "向量资产未就绪";
    default: return code || "嵌入模型不可用";
  }
}
