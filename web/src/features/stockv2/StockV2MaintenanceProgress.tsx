import { ArrowClockwise } from "@phosphor-icons/react";
import type { StockV2AssetMaintenanceJobProgress, StockV2AssetReadinessOverview, StockV2UpdateJob } from "../../app/types";
import { Button, Pill } from "../../components/ui";

export function stockV2AIProgressActive(job?: StockV2UpdateJob): boolean {
  const progress = job?.maintenanceProgress?.aiProfile;
  return progress?.status === "active" && progress.outstanding > 0;
}

export function StockV2ReadinessOverview({
  overview,
  loading,
  error,
  onRefresh,
}: {
  overview: StockV2AssetReadinessOverview | null;
  loading: boolean;
  error: string;
  onRefresh: () => void;
}) {
  const topReasons = Object.entries(overview?.reasonCounts ?? {})
    .sort((left, right) => right[1] - left[1] || left[0].localeCompare(right[0]))
    .slice(0, 4);
  const target = overview?.targetCount ?? 0;
  const gate = overview?.resourceGate;
  const majorUnavailable = overview?.limitationCounts?.major_announcement_content_status_unavailable ?? 0;
  return (
    <div aria-busy={loading} className="mt-3 border-t border-[var(--line-soft)] pt-3 text-xs">
      <div className="flex items-center justify-between gap-3">
        <span className="font-medium text-[var(--text)]">资产就绪度</span>
        <Button aria-label="刷新资产就绪度" className="px-2 py-1 text-xs" disabled={loading} onClick={onRefresh}>
          <ArrowClockwise className="mr-1" size={13} />
          {loading ? "刷新中" : "刷新"}
        </Button>
      </div>
      {error ? <div className="mt-2 text-[var(--danger)]" role="alert">{error}</div> : null}
      {!overview && loading ? <div className="mt-2 text-[var(--muted)]">正在计算当前维护集合的就绪度。</div> : null}
      {overview ? (
        <>
          <div className="mt-2 flex flex-wrap items-center gap-x-4 gap-y-2 text-[var(--muted-strong)]">
            <span>数据面 <strong className="font-mono text-[var(--text)]">{overview.marketReadyCount} / {target}</strong></span>
            <span>消息面 <strong className="font-mono text-[var(--text)]">{overview.messageReadyCount} / {target}</strong></span>
            <span>分析可用 <strong className="font-mono text-[var(--text)]">{overview.analysisReadyCount} / {target}</strong></span>
            {overview.expectedTradeDate ? <span>截止 <strong className="font-mono text-[var(--text)]">{overview.expectedTradeDate}</strong></span> : null}
            {gate ? <span className="inline-flex items-center gap-1.5">资源门禁 <Pill tone={resourceGateTone(gate.state)}>{resourceGateLabel(gate.state)}</Pill></span> : null}
          </div>
          {topReasons.length > 0 ? (
            <div className="mt-2 flex flex-wrap gap-x-3 gap-y-1 text-[var(--muted)]">
              <span>主要阻断</span>
              {topReasons.map(([code, count]) => (
                <span key={code}><span className="font-mono">{code}</span> <strong className="font-mono text-[var(--text)]">{count}</strong></span>
              ))}
            </div>
          ) : null}
          {!overview.announcementBodyParserAvailable && majorUnavailable > 0 ? (
            <div className="mt-2 text-[var(--warn)]">
              重大公告正文待处理 {majorUnavailable} 只标的；本机未检测到 <span className="font-mono">pdftotext</span>，严格分析保持阻断。
            </div>
          ) : null}
        </>
      ) : null}
    </div>
  );
}

function resourceGateTone(state: string): "good" | "warn" | "danger" | "neutral" {
  if (state === "paused") return "danger";
  if (state === "throttled") return "warn";
  if (state === "normal") return "good";
  return "neutral";
}

function resourceGateLabel(state: string): string {
  if (state === "paused") return "已暂停";
  if (state === "throttled") return "已限流";
  if (state === "normal") return "正常";
  return state || "未知";
}

export function StockV2MaintenanceProgress({ job, compact = false }: { job: StockV2UpdateJob; compact?: boolean }) {
  const progress = job.maintenanceProgress;
  const coverage = progress.coverage;
  const assets = progress.assets;
  const ai = progress.aiProfile;
  return (
    <div className={compact ? "text-xs" : "text-sm"}>
      <div className="grid gap-2 border-b border-[var(--line-soft)] pb-2 sm:grid-cols-[112px_minmax(0,1fr)] sm:items-center">
        <div className="flex items-center gap-2">
          <span className="font-medium text-[var(--text)]">覆盖检查</span>
          <Pill tone={coverageProgressTone(coverage)}>{coverageProgressLabel(coverage)}</Pill>
        </div>
        <div className="flex flex-wrap gap-x-4 gap-y-1 text-[var(--muted-strong)]">
          <span>
            已检查 <strong className="font-mono text-[var(--text)]">{coverage.checked}</strong>
            {coverage.target > 0 ? <span className="font-mono"> / {coverage.target}</span> : null}
          </span>
          {coverage.pending > 0 ? <span>待处理 <strong className="font-mono text-[var(--text)]">{coverage.pending}</strong></span> : null}
          {coverage.retrying > 0 ? <span>待重试 <strong className="font-mono text-[var(--warn)]">{coverage.retrying}</strong></span> : null}
          {coverage.failed > 0 ? <span>失败 <strong className="font-mono text-[var(--danger)]">{coverage.failed}</strong></span> : null}
        </div>
      </div>

      <div className="grid gap-2 border-b border-[var(--line-soft)] py-2 sm:grid-cols-[112px_minmax(0,1fr)] sm:items-center">
        <div className="flex items-center gap-2">
          <span className="font-medium text-[var(--text)]">基础资产</span>
          <Pill tone={assetsProgressTone(assets)}>{assetsProgressLabel(assets)}</Pill>
        </div>
        <div className="flex flex-wrap gap-x-4 gap-y-1 text-[var(--muted-strong)]">
          <span>可用 <strong className="font-mono text-[var(--good)]">{assets.fresh}</strong></span>
          <span>数据面 <strong className="font-mono text-[var(--text)]">{assets.marketFresh}</strong></span>
          <span>消息面 <strong className="font-mono text-[var(--text)]">{assets.messageFresh}</strong></span>
          {assets.stale > 0 ? <span>陈旧 <strong className="font-mono text-[var(--warn)]">{assets.stale}</strong></span> : null}
          {assets.retrying > 0 ? <span>待重试 <strong className="font-mono text-[var(--warn)]">{assets.retrying}</strong></span> : null}
          {assets.failed > 0 ? <span>失败 <strong className="font-mono text-[var(--danger)]">{assets.failed}</strong></span> : null}
        </div>
      </div>

      <div className="grid gap-2 pt-2 sm:grid-cols-[112px_minmax(0,1fr)] sm:items-center">
        <div className="flex items-center gap-2">
          <span className="font-medium text-[var(--text)]">AI 画像</span>
          <Pill tone={aiProgressTone(ai)}>{aiProgressLabel(ai)}</Pill>
        </div>
        <div className="flex flex-wrap gap-x-4 gap-y-1 text-[var(--muted-strong)]">
          {ai.requested > 0 ? (
            <span>已完成 <strong className="font-mono text-[var(--text)]">{ai.completed}</strong><span className="font-mono"> / {ai.requested}</span></span>
          ) : (
            <span>{ai.skipped > 0 ? `本轮跳过 ${ai.skipped}` : "本轮无需生成"}</span>
          )}
          {ai.pending > 0 ? <span>待接管 <strong className="font-mono text-[var(--text)]">{ai.pending}</strong></span> : null}
          {ai.queued > 0 ? <span>排队 <strong className="font-mono text-[var(--text)]">{ai.queued}</strong></span> : null}
          {ai.running > 0 ? <span>运行 <strong className="font-mono text-[var(--warn)]">{ai.running}</strong></span> : null}
          {ai.retrying > 0 ? <span>重试等待 <strong className="font-mono text-[var(--warn)]">{ai.retrying}</strong></span> : null}
          {ai.failed > 0 ? <span>失败 <strong className="font-mono text-[var(--danger)]">{ai.failed}</strong></span> : null}
        </div>
      </div>
    </div>
  );
}

function coverageProgressTone(progress: StockV2AssetMaintenanceJobProgress["coverage"]): "good" | "warn" | "danger" | "neutral" {
  if (progress.status === "failed") return "danger";
  if (progress.failed > 0 || progress.retrying > 0 || progress.status === "incomplete") return "warn";
  switch (progress.status) {
    case "covered": return "good";
    default: return "neutral";
  }
}

function coverageProgressLabel(progress: StockV2AssetMaintenanceJobProgress["coverage"]): string {
  if (progress.status === "failed") return "失败";
  if (progress.retrying > 0) return "等待重试";
  if (progress.failed > 0) return "部分失败";
  switch (progress.status) {
    case "covered": return "已覆盖";
    case "incomplete": return "未完整";
    case "pending": return "检查中";
    default: return progress.status || "未开始";
  }
}

function assetsProgressTone(progress: StockV2AssetMaintenanceJobProgress["assets"]): "good" | "warn" | "danger" | "neutral" {
  if (progress.status === "failed") return "danger";
  if (progress.failed > 0 || progress.retrying > 0 || progress.stale > 0) return "warn";
  switch (progress.status) {
    case "ready": return "good";
    case "stale":
    case "retrying": return "warn";
    default: return "neutral";
  }
}

function assetsProgressLabel(progress: StockV2AssetMaintenanceJobProgress["assets"]): string {
  if (progress.status === "failed") return "失败";
  if (progress.retrying > 0) return "等待重试";
  if (progress.failed > 0) return "部分失败";
  if (progress.stale > 0) return "存在陈旧";
  switch (progress.status) {
    case "ready": return "已就绪";
    case "stale": return "存在陈旧";
    case "retrying": return "等待重试";
    case "pending": return "处理中";
    default: return progress.status || "未开始";
  }
}

function aiProgressTone(progress: StockV2AssetMaintenanceJobProgress["aiProfile"]): "good" | "warn" | "danger" | "neutral" {
  if (progress.retrying > 0 || progress.status === "completed_with_failures") return "warn";
  switch (progress.status) {
    case "active": return "warn";
    case "completed": return "good";
    case "failed": return "danger";
    default: return "neutral";
  }
}

function aiProgressLabel(progress: StockV2AssetMaintenanceJobProgress["aiProfile"]): string {
  if (progress.retrying > 0) return "等待重试";
  switch (progress.status) {
    case "active": return "处理中";
    case "completed": return "已完成";
    case "completed_with_failures": return "部分失败";
    case "failed": return "失败";
    case "not_required": return "无需执行";
    default: return progress.status || "未开始";
  }
}
