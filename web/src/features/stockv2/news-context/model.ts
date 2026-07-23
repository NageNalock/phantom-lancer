import type {
  StockV2AgentModelProfile,
  StockV2AgentTaskProfile,
  StockV2NewsContextBackfillTask,
  StockV2NewsContextRun,
  StockV2NewsContextNamedObject,
  StockV2NewsContextRotationItem,
  StockV2NewsContextTheme,
  Tone,
} from "../../../app/types";

export const NEWS_CONTEXT_VIEWS = ["themes", "rotation", "aggregation", "cleanup"] as const;

export function themeTitle(theme?: Pick<StockV2NewsContextTheme, "id" | "title" | "name">): string {
  return theme?.title || theme?.name || theme?.id || "未命名主题";
}

export function rotationTitle(item: StockV2NewsContextRotationItem): string {
  return item.title || item.themeId || item.id || "未命名线索";
}

export function namedObjectLabel(value: string | StockV2NewsContextNamedObject): string {
  if (typeof value === "string") return value;
  const name = value.name || value.title || "";
  return [value.symbol, name].filter(Boolean).join(" ") || value.id || "未知对象";
}

export function formatNewsContextTime(value?: string): string {
  if (!value) return "-";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString("zh-CN", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  });
}

export function formatNewsContextBytes(value?: number): string {
  const bytes = Number(value || 0);
  if (!Number.isFinite(bytes) || bytes <= 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const index = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1);
  const amount = bytes / 1024 ** index;
  return `${amount >= 10 || index === 0 ? amount.toFixed(0) : amount.toFixed(1)} ${units[index]}`;
}

export function formatNewsContextInterval(value?: number): string {
  const seconds = Number(value || 0);
  if (!Number.isFinite(seconds) || seconds <= 0) return "-";
  if (seconds % 86400 === 0) return `${seconds / 86400} 天`;
  if (seconds % 3600 === 0) return `${seconds / 3600} 小时`;
  if (seconds % 60 === 0) return `${seconds / 60} 分钟`;
  return `${seconds} 秒`;
}

export function resolveAvailableTaskModel(
  taskType: string,
  taskProfiles: StockV2AgentTaskProfile[],
  models: StockV2AgentModelProfile[],
): StockV2AgentModelProfile | undefined {
  const profile = taskProfiles.find((item) => item.taskType === taskType);
  return [profile?.primaryModelId, profile?.fallbackModelId]
    .map((id) => models.find((model) => model.id === id))
    .find((model) => model?.enabled && model.status === "available" && model.modelType === "chat");
}

export function newsContextRunCoverage(run: StockV2NewsContextRun): {
  empty: boolean;
  total: number;
  covered: number;
  noise: number;
  deferred: number;
  waiting: number;
  percent: number;
} {
  const hasTotal = typeof run.totalNewsCount === "number" && Number.isFinite(run.totalNewsCount);
  const total = hasTotal ? Math.max(0, Number(run.totalNewsCount)) : 0;
  const processed = Math.max(0, run.processedNewsCount ?? 0);
  const empty = Boolean(run.windowType) && run.status === "completed" && hasTotal && total === 0;
  const rawProgress = empty ? 0 : typeof run.progress === "number" && Number.isFinite(run.progress)
    ? run.progress
    : total > 0 ? processed / total : 0;
  return {
    empty,
    total,
    covered: Math.max(0, run.coveredCount ?? 0),
    noise: Math.max(0, run.noiseCount ?? 0),
    deferred: Math.max(0, run.deferredCount ?? 0),
    waiting: Math.max(0, run.pendingThemeCount ?? total - processed),
    percent: Math.round(Math.max(0, Math.min(1, rawProgress)) * 100),
  };
}

export function newsContextBackfillNeedsRetry(task?: StockV2NewsContextBackfillTask | null): boolean {
  return task?.status === "failed" || (task?.status === "completed" && task.missingNewsCount > 0);
}

export function newsContextFinalReviewCoverage(task?: StockV2NewsContextBackfillTask | null): {
  output: number;
  linked: number;
  missing: number;
} | null {
  const values = [
    task?.historicalDailyOutputVersionCount,
    task?.finalReviewLinkedVersionCount,
    task?.finalReviewMissingVersionCount,
  ];
  if (!values.every((value) => typeof value === "number" && Number.isFinite(value))) return null;
  return {
    output: Math.max(0, values[0] as number),
    linked: Math.max(0, values[1] as number),
    missing: Math.max(0, values[2] as number),
  };
}

export function originalNewsStatus(deleted?: boolean): { label: string; tone: Tone } {
  if (deleted === true) return { label: "原文已清理", tone: "neutral" };
  if (deleted === false) return { label: "原文保留", tone: "good" };
  return { label: "原文状态未知", tone: "neutral" };
}

export function confidenceLabel(value?: number): string {
  if (typeof value !== "number" || !Number.isFinite(value)) return "未评估";
  const normalized = value <= 1 ? value * 100 : value;
  return `${Math.max(0, Math.min(100, normalized)).toFixed(0)}%`;
}

export function themeStageLabel(value?: string): string {
  return ({
    emerging: "萌芽",
    germination: "萌芽",
    spreading: "扩散",
    diffusion: "扩散",
    accelerating: "加速",
    acceleration: "加速",
    overheated: "过热",
    diverging: "分歧",
    retreating: "退潮",
    decline: "退潮",
    dormant: "沉寂",
    restarting: "再启动",
    restart: "再启动",
  } as Record<string, string>)[value || ""] || value || "未判断";
}

export function themeStageTone(value?: string): Tone {
  if (["accelerating", "acceleration", "restarting", "restart"].includes(value || "")) return "good";
  if (["overheated", "diverging", "retreating"].includes(value || "")) return "warn";
  return "neutral";
}

export function reviewStatusLabel(value?: string): string {
  return ({
    pending: "等待复核",
    running: "复核中",
    completed: "已复核",
    not_required: "无需复核",
    skipped: "无需复核",
    failed: "复核失败",
  } as Record<string, string>)[value || ""] || value || "未复核";
}

export function reviewStatusTone(value?: string): Tone {
  if (value === "completed" || value === "not_required" || value === "skipped") return "good";
  if (value === "pending" || value === "running") return "warn";
  if (value === "failed") return "danger";
  return "neutral";
}

export function researchStatusLabel(value?: string): string {
  return ({
    not_required: "无需公开核实",
    completed: "公开核实完成",
    verified: "公开核实完成",
    unresolved: "公开核实有未决项",
    unavailable: "公开搜索不可用",
    failed: "公开搜索失败",
  } as Record<string, string>)[value || ""] || value || "未记录公开核实";
}

export function researchStatusTone(value?: string): Tone {
  if (value === "completed" || value === "verified" || value === "not_required") return "good";
  if (value === "unresolved" || value === "unavailable") return "warn";
  if (value === "failed") return "danger";
  return "neutral";
}

export function indexStatusLabel(value?: string): string {
  return ({
    ready: "索引可用",
    missing: "等待索引",
    pending: "等待索引",
    stale: "索引过期",
    failed: "索引失败",
    unavailable: "索引不可用",
  } as Record<string, string>)[value || ""] || value || "索引未知";
}

export function indexStatusTone(value?: string): Tone {
  if (value === "ready") return "good";
  if (value === "missing" || value === "pending" || value === "stale") return "warn";
  if (value === "failed" || value === "unavailable") return "danger";
  return "neutral";
}

export function mcpVerificationLabel(available?: boolean, toolsReady?: boolean, status?: string): string {
  if (!available || !toolsReady) return "不可用";
  if (status === "ready") return "已验证";
  if (status === "failed") return "验证失败";
  return "待真实验证";
}

export function mcpVerificationTone(available?: boolean, toolsReady?: boolean, status?: string): Tone {
  if (!available || !toolsReady || status === "failed") return "danger";
  if (status === "ready") return "good";
  return "warn";
}

export function runStatusLabel(value?: string): string {
  return ({
    pending: "等待执行",
    queued: "排队中",
    running: "执行中",
    waiting_review: "等待复核",
    partial: "部分完成",
    completed: "已完成",
    failed: "失败",
    paused: "已暂停",
    cancelled: "已取消",
  } as Record<string, string>)[value || ""] || value || "未知";
}

export function runStatusTone(value?: string): Tone {
  if (value === "completed") return "good";
  if (value === "pending" || value === "queued" || value === "running" || value === "waiting_review" || value === "partial") return "warn";
  if (value === "failed") return "danger";
  return "neutral";
}

export function backfillPhaseLabel(value?: string): string {
  return ({
    queued: "等待调度",
    preflight: "运行前检查",
    normalizing: "标准化历史消息",
    hourly: "重建小时检查点",
    four_hour: "重建四小时模型归纳",
    daily: "重建日级增量物化",
    late_scan: "检查迟到与遗漏消息",
    final_daily: "物化当前日结论",
    indexing: "完成主题索引",
    final_review: "完成组合影响复核",
    mcp_verification: "验证 CLI 检索",
    finalizing: "执行最终安全校验",
    paused: "已暂停",
    failed: "失败",
    completed: "全部完成",
  } as Record<string, string>)[value || ""] || value || "等待开始";
}

export function backfillStatusLabel(value?: string): string {
  return ({
    running: "补处理中",
    paused: "已暂停",
    failed: "失败",
    completed: "已完成",
  } as Record<string, string>)[value || ""] || value || "尚未启动";
}

export function backfillStatusTone(value?: string): Tone {
  if (value === "completed") return "good";
  if (value === "running" || value === "paused") return "warn";
  if (value === "failed") return "danger";
  return "neutral";
}

export function backfillStageLabel(value?: string): string {
  return ({
    hourly: "小时检查点",
    four_hour: "四小时模型归纳",
    daily: "日级增量物化",
    late_scan: "迟到与遗漏扫描",
    final_daily: "当前日结论增量物化",
    indexing: "主题索引",
    final_review: "组合影响复核",
    finalizing: "最终安全校验",
  } as Record<string, string>)[value || ""] || value || "未知阶段";
}

export function backfillStageStatusLabel(value?: string): string {
  return ({
    pending: "待开始",
    queued: "排队中",
    running: "进行中",
    paused: "已暂停",
    failed: "失败",
    completed: "已完成",
  } as Record<string, string>)[value || ""] || value || "未知";
}

export function backfillStageStatusTone(value?: string): Tone {
  if (value === "completed") return "good";
  if (value === "running" || value === "queued" || value === "paused") return "warn";
  if (value === "failed") return "danger";
  return "neutral";
}

export function backfillRunPhaseLabel(value?: string): string {
  return ({
    collecting: "收集输入",
    aggregating: "分批归纳",
    queued: "等待下一批",
    checkpointing: "写入检查点",
    checkpointed: "检查点已写入",
    materializing: "增量物化",
    materialized: "增量物化完成",
    indexing: "更新索引",
    waiting_review: "等待复核",
    completed: "已完成",
    failed: "失败",
  } as Record<string, string>)[value || ""] || value || "";
}

export function coverageStatusLabel(value?: string): string {
  return ({
    complete: "覆盖完整",
    partial: "部分覆盖",
    missing: "存在缺口",
    pending: "等待覆盖",
    failed: "覆盖失败",
  } as Record<string, string>)[value || ""] || value || "覆盖未知";
}

export function coverageStatusTone(value?: string): Tone {
  if (value === "complete") return "good";
  if (value === "partial" || value === "missing" || value === "pending") return "warn";
  if (value === "failed") return "danger";
  return "neutral";
}

export function windowTypeLabel(value?: string): string {
  return ({
    hourly: "每小时",
    hour: "每小时",
    four_hour: "每四小时",
    four_hourly: "每四小时",
    daily: "每日",
    cleanup: "安全清理",
  } as Record<string, string>)[value || ""] || value || "未指定周期";
}

export function confirmationLabel(value?: string): string {
  return ({
    news_only: "仅有消息线索",
    awaiting_market: "等待行情确认",
    market_confirmed: "行情已确认",
    multi_source_confirmed: "多类数据确认",
    contradicted: "数据未支持",
  } as Record<string, string>)[value || ""] || value || "等待数据确认";
}

export function confirmationTone(value?: string): Tone {
  if (value === "market_confirmed" || value === "multi_source_confirmed") return "good";
  if (value === "contradicted") return "danger";
  return "warn";
}

export function evidenceRoleLabel(value?: string): string {
  return ({
    support: "支持",
    weaken: "削弱",
    contradict: "反驳",
    background: "背景",
  } as Record<string, string>)[value || ""] || value || "背景";
}

export function relationTypeLabel(value?: string): string {
  return ({
    upstream: "上游",
    downstream: "下游",
    substitute: "替代",
    competition: "竞争",
    spillover: "资金外溢",
    rotation: "轮换承接",
    support: "支持",
    contradict: "反驳",
    related: "可能相关",
  } as Record<string, string>)[value || ""] || value || "可能相关";
}
