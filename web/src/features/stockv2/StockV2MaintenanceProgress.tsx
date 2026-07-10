import type { StockV2AssetMaintenanceJobProgress, StockV2UpdateJob } from "../../app/types";
import { Pill } from "../../components/ui";
import { stockV2UpdateStatusLabel, stockV2UpdateStatusTone } from "../../domain/labels";

export function stockV2AIProgressActive(job?: StockV2UpdateJob): boolean {
  const progress = job?.maintenanceProgress?.aiProfile;
  return progress?.status === "active" && progress.outstanding > 0;
}

export function StockV2MaintenanceProgress({ job, compact = false }: { job: StockV2UpdateJob; compact?: boolean }) {
  const progress = maintenanceProgress(job);
  const ai = progress.aiProfile;
  return (
    <div className={compact ? "text-xs" : "text-sm"}>
      <div className="grid gap-2 border-b border-[var(--line-soft)] pb-2 sm:grid-cols-[112px_minmax(0,1fr)] sm:items-center">
        <div className="flex items-center gap-2">
          <span className="font-medium text-[var(--text)]">基础维护</span>
          <Pill tone={baseProgressTone(job)}>{stockV2UpdateStatusLabel(job)}</Pill>
        </div>
        <div className="flex flex-wrap gap-x-4 gap-y-1 text-[var(--muted-strong)]">
          <span>
            已处理 <strong className="font-mono text-[var(--text)]">{progress.base.processed}</strong>
            {progress.base.total > 0 ? <span className="font-mono"> / {progress.base.total}</span> : null}
          </span>
          <span>成功 <strong className="font-mono text-[var(--good)]">{progress.base.succeeded}</strong></span>
          <span>失败 <strong className={progress.base.failed > 0 ? "font-mono text-[var(--danger)]" : "font-mono text-[var(--text)]"}>{progress.base.failed}</strong></span>
          {progress.base.pending > 0 ? <span>待处理 <strong className="font-mono text-[var(--text)]">{progress.base.pending}</strong></span> : null}
        </div>
      </div>

      <div className="grid gap-2 pt-2 sm:grid-cols-[112px_minmax(0,1fr)] sm:items-center">
        <div className="flex items-center gap-2">
          <span className="font-medium text-[var(--text)]">AI 画像</span>
          <Pill tone={aiProgressTone(ai.status)}>{aiProgressLabel(ai.status)}</Pill>
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

function maintenanceProgress(job: StockV2UpdateJob): StockV2AssetMaintenanceJobProgress {
  if (job.maintenanceProgress) return job.maintenanceProgress;
  const pending = Math.max(0, (job.totalCount || 0) - (job.processedCount || 0));
  const requested = job.assetStats?.aiCalled || 0;
  const queued = job.assetStats?.aiQueued || 0;
  const running = job.assetStats?.aiRunning || 0;
  const completed = job.assetStats?.aiCompleted || 0;
  const failed = job.assetStats?.aiFailed || 0;
  const outstanding = queued + running;
  const aiStatus = requested === 0
    ? "not_required"
    : outstanding > 0 || completed + failed < requested
      ? "active"
      : failed > 0
        ? "completed_with_failures"
        : "completed";
  return {
    base: {
      status: job.status,
      total: job.totalCount || 0,
      processed: job.processedCount || 0,
      succeeded: job.successCount || 0,
      failed: job.failedCount || 0,
      pending,
    },
    aiProfile: {
      status: aiStatus,
      requested,
      pending: 0,
      queued,
      running,
      retrying: 0,
      completed,
      failed,
      skipped: job.assetStats?.aiSkipped || 0,
      outstanding,
    },
  };
}

function baseProgressTone(job: StockV2UpdateJob): "good" | "warn" | "danger" | "neutral" {
  if (job.status === "completed" && job.failedCount > 0) return "warn";
  return stockV2UpdateStatusTone(job);
}

function aiProgressTone(status: string): "good" | "warn" | "danger" | "neutral" {
  switch (status) {
    case "active": return "warn";
    case "completed": return "good";
    case "completed_with_failures": return "danger";
    default: return "neutral";
  }
}

function aiProgressLabel(status: string): string {
  switch (status) {
    case "active": return "处理中";
    case "completed": return "已完成";
    case "completed_with_failures": return "部分失败";
    case "not_required": return "无需执行";
    default: return status || "未开始";
  }
}
