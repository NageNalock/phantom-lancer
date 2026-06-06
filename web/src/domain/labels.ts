import type { AuditEvent, CodexGatewaySettings, CodexGatewayStatus, ImageProviderSettings, ImageStatus, ImageStorageSettings, PermissionProfile, V2RaySettings, V2RayStatus, Workspace } from "../app/types";

export const CODEX_TABS = [
  { id: "sessions", label: "会话", description: "长期会话、实时事件和任务上下文" },
  { id: "projects", label: "项目", description: "受控工作区、默认权限和路径边界" },
  { id: "permissions", label: "权限", description: "能力 profile、风险等级和审批边界" },
  { id: "gateway", label: "Gateway", description: "Codex OAuth 账号、OpenAI 兼容端点和请求审计" },
  { id: "activity", label: "活动", description: "登录、配置和执行历史" },
] as const;

export const NAV_ITEMS = [
  { id: "dashboard", label: "控制台", short: "01", description: "服务器状态、执行边界和下一步入口" },
  { id: "codex", label: "Codex", short: "02", description: "会话、项目、权限和活动统一收敛在 Codex 工作区" },
  { id: "logs", label: "日志", short: "03", description: "服务日志、运行事件和在线排障视图" },
  { id: "images", label: "Images", short: "04", description: "xAI Grok Imagine 生成、编辑、图片库、历史和存储设置" },
  { id: "v2ray", label: "V2Ray", short: "05", description: "内嵌 V2Ray 服务端、远程设备接入和运行控制" },
  { id: "settings", label: "设置", short: "06", description: "运行期配置、允许根目录和 CLI 探针" },
] as const;

const profileLabels: Record<string, string> = {
  Observe: "观察",
  Maintain: "维护",
  Deploy: "部署",
  Admin: "管理",
  Emergency: "紧急",
};

const eventLabels: Record<string, string> = {
  "session.created": "会话已创建",
  "session.failed": "会话启动失败",
  "thread.attached": "已连接 Codex thread",
  "thread.resumed": "已恢复 Codex thread",
  "thread/started": "Codex thread 已启动",
  "thread/status/changed": "会话状态变化",
  "thread/archived": "会话已归档",
  "thread.archived.local": "会话已归档",
  "turn.submitted": "已发送提示词",
  "turn.steered": "已追加引导",
  "turn.start.failed": "回合启动失败",
  "turn.steer.failed": "追加引导失败",
  "turn.interrupt.requested": "已请求中断",
  "turn/started": "回合已开始",
  "turn/completed": "回合已结束",
  "turn/diff/updated": "文件差异更新",
  "turn/plan/updated": "计划更新",
  "item/started": "条目开始",
  "item/completed": "条目完成",
  "item/agentMessage/delta": "回复增量",
  "item/commandExecution/outputDelta": "命令输出",
  "item/fileChange/outputDelta": "文件变更输出",
  "item/fileChange/patchUpdated": "补丁更新",
  "item/reasoning/summaryTextDelta": "推理摘要",
  error: "Codex 错误",
  warning: "Codex 警告",
};

const auditLabels: Record<string, string> = {
  "owner.bootstrap": "初始化管理员",
  "auth.login": "登录",
  "workspace.create": "添加项目",
  "codex.exec.start": "启动 Codex 任务",
  "codex.session.create": "创建 Codex 会话",
  "codex.turn.send": "发送 Codex 对话",
  "codex.turn.interrupt": "中断 Codex 回合",
  "codex.session.archive": "归档 Codex 会话",
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
};

export function profileLabel(value?: string): string {
  return profileLabels[value || ""] || value || "观察";
}

export function eventLabel(type?: string): string {
  return eventLabels[type || ""] || type || "事件";
}

export function auditLabel(type?: string): string {
  return auditLabels[type || ""] || type || "审计事件";
}

export function sandboxLabel(value?: string): string {
  return value === "workspace-write" ? "工作区可写" : "只读";
}

export function sessionStatusLabel(value?: string): string {
  return (
    {
      starting: "启动中",
      idle: "空闲",
      active: "运行中",
      failed: "失败",
      archived: "已归档",
      closed: "已关闭",
    }[value || ""] ||
    value ||
    "未知"
  );
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

export function workspaceName(workspace?: Workspace): string {
  return workspace?.name || "未知项目";
}

export function defaultRuntime() {
  return { allowedRoots: [], codexBinary: "codex", codexHome: "", cookieSecure: false, updatedAt: "" };
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
    oauthClientId: "",
    oauthRedirectUri: "",
    requestTimeoutSeconds: 600,
    refreshMarginSeconds: 300,
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

export function defaultProfiles(): PermissionProfile[] {
  return ["Observe", "Maintain", "Deploy", "Admin"].map((name) => ({
    name,
    risk: name === "Observe" ? "low" : name === "Maintain" ? "medium" : "high",
    description: "该权限模式待接入策略编辑。",
  }));
}

export function auditSummary(event: AuditEvent): string {
  return event.summary || auditLabel(event.eventType);
}

export function maskSecret(value?: string): string {
  const text = value || "";
  if (!text) return "-";
  return text.length <= 12 ? "****" : `${text.slice(0, 8)}...${text.slice(-6)}`;
}
