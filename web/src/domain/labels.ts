import type { AuditEvent, CodexGatewaySettings, CodexGatewayStatus, CodexStatus, ImageProviderSettings, ImageStatus, ImageStorageSettings, V2RaySettings, V2RayStatus } from "../app/types";

export const NAV_ITEMS = [
  { id: "dashboard", label: "控制台", short: "01", description: "服务器状态、执行边界和下一步入口" },
  { id: "codex", label: "Codex", short: "02", description: "本机 codex CLI 会话、工作区、审批和运行诊断" },
  { id: "codex-gateway", label: "Codex Gateway", short: "03", description: "Codex OAuth 账号、OpenAI 兼容端点和请求审计" },
  { id: "logs", label: "日志", short: "04", description: "服务日志、运行事件和在线排障视图" },
  { id: "images", label: "Images", short: "05", description: "xAI Grok Imagine 生成、编辑、图片库、历史和存储设置" },
  { id: "docker", label: "Docker", short: "06", description: "Docker 守护进程、镜像与容器生命周期、daemon 安装与控制、内嵌 Registry" },
  { id: "v2ray", label: "V2Ray", short: "07", description: "内嵌 V2Ray 服务端、远程设备接入和运行控制" },
  { id: "settings", label: "设置", short: "08", description: "运行期配置、允许根目录和全局安全策略" },
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
  "images.settings.update": "更新 Images 设置",
  "images.storage.update": "更新 Images 存储",
  "images.storage.settings.updated": "更新 Images 存储设置",
  "images.storage.tested": "测试 Images 对象存储",
  "images.job.completed": "Images 调用完成",
  "images.job.failed": "Images 调用失败",
  "images.asset.source_uploaded": "参考图已入库",
  "images.asset.stored.local": "图片已保存到本地",
  "images.asset.stored.s3": "图片已保存到对象存储",
  "images.asset.deleted": "图片已删除",
  "images.asset.archived.s3": "图片已归档到对象存储",
  "images.asset.store_failed": "图片保存失败",
  "images.asset.private.added": "加入私密收藏夹",
  "images.asset.private.removed": "移出私密收藏夹",
  "images.private.unlocked": "解锁 Images 私密收藏夹",
  "images.private.locked": "锁定 Images 私密收藏夹",
  "images.private.rate_limited": "Images 私密解锁限流",
  "images.private.backoff_started": "Images 私密解锁退避",
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
