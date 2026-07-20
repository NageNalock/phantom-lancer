import { useEffect, useMemo, useRef, useState } from "react";
import { ArrowClockwise, Pause, Play, Repeat } from "@phosphor-icons/react";
import type { AppActions } from "../../../app/App";
import type {
  StockV2NewsContextBackfillPreview,
  StockV2NewsContextBackfillStageProgress,
  StockV2NewsContextBackfillTask,
} from "../../../app/types";
import { friendlyError } from "../../../api/client";
import { Button, EmptyState, Notice, Pill, useDangerConfirm } from "../../../components/ui";
import {
  backfillPhaseLabel,
  backfillRunPhaseLabel,
  backfillStageLabel,
  backfillStageStatusLabel,
  backfillStageStatusTone,
  backfillStatusLabel,
  backfillStatusTone,
  formatNewsContextTime,
  newsContextBackfillNeedsRetry,
  newsContextFinalReviewCoverage,
} from "./model";
import { startSequentialPolling } from "./polling";

export function NewsContextBackfillPanel({
  actions,
  refreshKey,
  onChanged,
}: {
  actions: AppActions;
  refreshKey: number;
  onChanged: () => void;
}) {
  const [preview, setPreview] = useState<StockV2NewsContextBackfillPreview | null>(null);
  const [task, setTask] = useState<StockV2NewsContextBackfillTask | null>(null);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const taskRequestRef = useRef<Promise<StockV2NewsContextBackfillTask | null> | null>(null);
  const { confirmDanger, dangerConfirmDialog } = useDangerConfirm();

  function requestTask() {
    if (!taskRequestRef.current) {
      taskRequestRef.current = loadBackfillTask(actions).finally(() => {
        taskRequestRef.current = null;
      });
    }
    return taskRequestRef.current;
  }

  async function load(initial = false) {
    if (initial && !preview && !task) setLoading(true);
    else setRefreshing(true);
    setError(null);
    try {
      const [nextPreview, nextTask] = await Promise.all([
        actions.api<StockV2NewsContextBackfillPreview>("/api/stockv2/news-context/backfill/preview"),
        requestTask(),
      ]);
      setPreview(nextPreview);
      setTask(nextTask);
    } catch (loadError) {
      setError(friendlyError(loadError));
    } finally {
      setLoading(false);
      setRefreshing(false);
    }
  }

  useEffect(() => {
    void load(true);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [refreshKey]);

  const taskRunning = task?.status === "running";
  useEffect(() => {
    if (!taskRunning) return;
    return startSequentialPolling(async (signal) => {
      try {
        const nextTask = await requestTask();
        if (signal.aborted) return;
        setTask(nextTask);
        setError(null);
      } catch (loadError) {
        if (!signal.aborted) setError(friendlyError(loadError));
      }
    }, 5000);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [taskRunning]);

  async function start() {
    if (!preview) return;
    const range = preview.earliestNewsAt || preview.latestNewsAt
      ? `${formatNewsContextTime(preview.earliestNewsAt)} 至 ${formatNewsContextTime(preview.latestNewsAt)}`
      : "待后端确认";
    const chunks = typeof preview.estimatedChunkCount === "number" ? `${preview.estimatedChunkCount} 个` : "待后端评估";
    const confirmed = await confirmDanger({
      title: "开始历史补处理",
      body: `系统将处理 ${preview.pendingNewsCount} 条历史新闻；范围 ${range}，启动时截止点固定到最近完整小时，预计 ${chunks} 自动分片。`,
      impact: ["实时归纳始终优先，历史任务会在每个自动分片后让出执行位", "本阶段只归纳和建立索引，不清理原新闻"],
      recovery: "可以在当前分片完成后暂停，继续和失败重试都会使用原任务进度。",
      confirmLabel: "开始补处理",
    });
    if (!confirmed) return;
    await runAction("start", "/api/stockv2/news-context/backfill", "历史补处理已启动");
  }

  async function runAction(action: "start" | "pause" | "resume" | "retry", path: string, successMessage: string) {
    setBusy(action);
    setError(null);
    try {
      const nextTask = await actions.api<StockV2NewsContextBackfillTask>(path, {
        method: "POST",
        csrf: actions.csrf,
      });
      setTask(nextTask);
      actions.setToast(successMessage, "good");
      await load();
      onChanged();
    } catch (actionError) {
      const message = friendlyError(actionError);
      setError(message);
      actions.setToast(message, "danger");
    } finally {
      setBusy(null);
    }
  }

  const counts = useMemo(() => ({
    total: task?.totalNewsCount ?? preview?.pendingNewsCount ?? preview?.totalNewsCount ?? 0,
    processed: task?.processedNewsCount ?? 0,
    remaining: task?.remainingNewsCount ?? preview?.pendingNewsCount ?? 0,
    missing: task?.missingNewsCount ?? 0,
  }), [preview, task]);
  const canStart = Boolean(
    preview
    && preview.prerequisitesReady
    && preview.pendingNewsCount > 0
    && (!task || (task.status === "completed" && task.missingNewsCount === 0)),
  );
  const blockingReasons = preview?.blockingReasons || [];
  const needsRetry = newsContextBackfillNeedsRetry(task);
  const finalReviewCoverage = newsContextFinalReviewCoverage(task);
  const currentStage = task?.stageProgress?.find((stage) => stage.status === "running" || stage.status === "paused");

  return (
    <>
      <section aria-busy={loading || refreshing} className="overflow-hidden rounded-lg border border-[var(--line)] bg-[var(--surface)]">
        <header className="flex flex-wrap items-start justify-between gap-3 border-b border-[var(--line)] px-4 py-3">
          <div>
            <div className="flex flex-wrap items-center gap-2">
              <h2 className="m-0 text-sm font-semibold">历史新闻补处理</h2>
              {task ? <Pill tone={backfillStatusTone(task.status)}>{backfillStatusLabel(task.status)}</Pill> : null}
              {task?.phase ? <Pill tone="neutral">{currentStage ? backfillStageLabel(currentStage.phase) : backfillPhaseLabel(task.phase)}</Pill> : null}
            </div>
            <p className="mt-1 mb-0 text-xs text-[var(--muted)]">从旧到新重建消息脉络，不限制新闻和主题总量，实时定时任务始终优先。</p>
          </div>
          <div className="flex flex-wrap items-center justify-end gap-2">
            {taskRunning ? (
              <Button disabled={Boolean(busy)} onClick={() => void runAction("pause", "/api/stockv2/news-context/backfill/pause", "将在当前分片完成后暂停") }>
                <Pause size={14} />{busy === "pause" ? "暂停中" : "暂停"}
              </Button>
            ) : null}
            {task?.status === "paused" ? (
              <Button disabled={Boolean(busy)} onClick={() => void runAction("resume", "/api/stockv2/news-context/backfill/resume", "历史补处理已继续")} tone="primary">
                <Play size={14} />{busy === "resume" ? "继续中" : "继续"}
              </Button>
            ) : null}
            {needsRetry ? (
              <Button
                disabled={Boolean(busy)}
                onClick={() => void runAction(
                  "retry",
                  "/api/stockv2/news-context/backfill/retry",
                  task?.status === "completed" ? "已开始重试遗漏新闻" : "已从失败阶段重试",
                )}
                tone="primary"
              >
                <Repeat size={14} />{busy === "retry" ? "重试中" : task?.status === "completed" ? "重试遗漏" : "从失败处重试"}
              </Button>
            ) : null}
            {(!task || (task.status === "completed" && task.missingNewsCount === 0)) && (preview?.pendingNewsCount || 0) > 0 ? (
              <Button disabled={!canStart || Boolean(busy)} onClick={() => void start()} tone="primary">
                <Play size={14} />{busy === "start" ? "启动中" : "开始补处理"}
              </Button>
            ) : null}
            <Button disabled={refreshing || loading || Boolean(busy)} onClick={() => void load()}>
              <ArrowClockwise size={14} />{refreshing ? "刷新中" : "刷新"}
            </Button>
          </div>
        </header>

        {error ? (
          <div className="border-b border-[var(--line)] p-3">
            <Notice tone="danger">
              <span className="flex flex-wrap items-center justify-between gap-2">
                <span>历史补处理请求失败：{error}{preview || task ? "。当前保留上次结果。" : ""}</span>
                <Button onClick={() => void load(true)}>重试</Button>
              </span>
            </Notice>
          </div>
        ) : null}

        {loading && !preview && !task ? <BackfillSkeleton /> : null}

        {!loading && preview && !task && preview.pendingNewsCount === 0 ? (
          <div className="p-4">
            <EmptyState title="没有待补处理的历史新闻" body="新新闻继续由小时、四小时和每日定时归纳处理。" />
          </div>
        ) : null}

        {preview || task ? (
          <div>
            <div className="grid grid-cols-4 divide-x divide-[var(--line)] border-b border-[var(--line)] max-lg:grid-cols-2 max-lg:divide-x-0">
              <BackfillMetric label="原始新闻总量" value={counts.total} />
              <BackfillMetric label="小时级已覆盖" value={counts.processed} />
              <BackfillMetric label="小时级待覆盖" value={counts.remaining} tone={counts.remaining ? "warn" : "neutral"} />
              <BackfillMetric label="覆盖异常" value={counts.missing} tone={counts.missing ? "danger" : "neutral"} />
            </div>

            {task?.stageProgress?.length ? <BackfillStageProgressSection stages={task.stageProgress} /> : null}

            <div className="grid grid-cols-2 gap-x-8 gap-y-2 px-4 py-3 text-xs max-lg:grid-cols-1">
              <BackfillDetail
                label="历史范围"
                value={task?.rangeStartAt || task?.cutoffAt
                  ? `${formatNewsContextTime(task.rangeStartAt)} 至 ${formatNewsContextTime(task.cutoffAt)}`
                  : `${formatNewsContextTime(preview?.earliestNewsAt)} 至 ${formatNewsContextTime(preview?.latestNewsAt)}`}
              />
              <BackfillDetail label="固定截止点" value={task?.cutoffAt ? formatNewsContextTime(task.cutoffAt) : "启动时固定到最近完整小时"} />
              {preview ? <BackfillDetail label="历史新闻总量" value={`${preview.totalNewsCount} 条，待补处理 ${preview.pendingNewsCount} 条`} /> : null}
              <BackfillDetail
                label="当前窗口"
                value={task?.currentWindowStart || task?.currentWindowEnd
                  ? `${formatNewsContextTime(task.currentWindowStart)} 至 ${formatNewsContextTime(task.currentWindowEnd)}`
                  : "等待执行"}
              />
              <BackfillDetail label="最近更新" value={formatNewsContextTime(task?.updatedAt)} />
              {task ? <BackfillDetail label="分片进度" value={`已完成 ${task.completedChunkCount || 0} 个自动分片`} /> : null}
              {preview?.estimatedChunkCount ? <BackfillDetail label="预计自动分片" value={`${preview.estimatedChunkCount} 个，仅用于运行稳定性`} /> : null}
              {task?.currentRunId ? <BackfillDetail label="当前归纳运行" value={task.currentRunId} /> : null}
              {task?.finalReviewRunId ? <BackfillDetail label="最终复核运行" value={task.finalReviewRunId} /> : null}
              {finalReviewCoverage ? (
                <BackfillDetail
                  label="最终复核覆盖"
                  value={`${finalReviewCoverage.linked} / ${finalReviewCoverage.output} 个历史每日输出已关联；遗漏 ${finalReviewCoverage.missing} 个`}
                />
              ) : task?.finalReviewRunId ? <BackfillDetail label="最终复核覆盖" value="等待覆盖统计" /> : null}
              {task?.completedAt ? <BackfillDetail label="完成时间" value={formatNewsContextTime(task.completedAt)} /> : null}
            </div>

            {blockingReasons.length ? (
              <div className="border-t border-[var(--line)] p-3">
                <Notice tone="danger">暂时不能启动：{blockingReasons.join("；")}</Notice>
              </div>
            ) : null}
            {preview?.prerequisitesReady && !blockingReasons.length ? (
              <div className="flex flex-wrap items-center justify-between gap-2 border-t border-[var(--line)] px-4 py-3 text-xs">
                <span className="text-[var(--muted)]">消息归纳、影响复核和主题向量条件</span>
                <Pill tone="good">已就绪</Pill>
              </div>
            ) : null}
            {task?.errorMessage ? (
              <div className="border-t border-[var(--line)] p-3">
                <Notice tone="danger">{task.phase ? `${backfillPhaseLabel(task.phase)}：` : ""}{task.errorMessage}</Notice>
              </div>
            ) : null}
            {counts.missing > 0 ? (
              <div className="border-t border-[var(--line)] p-3">
                <Notice tone="danger">仍有 {counts.missing} 条新闻没有明确覆盖结果，任务完成和新闻清理都会保持锁定。</Notice>
              </div>
            ) : null}
            {finalReviewCoverage && finalReviewCoverage.missing > 0 ? (
              <div className="border-t border-[var(--line)] p-3">
                <Notice tone="danger">最终复核仍有 {finalReviewCoverage.missing} 个历史每日输出未关联，新闻清理会保持锁定。</Notice>
              </div>
            ) : null}
          </div>
        ) : null}
      </section>
      {dangerConfirmDialog}
    </>
  );
}

async function loadBackfillTask(actions: AppActions): Promise<StockV2NewsContextBackfillTask | null> {
  try {
    return await actions.api<StockV2NewsContextBackfillTask>("/api/stockv2/news-context/backfill");
  } catch (error) {
    // ponytail: 尚无任务是合法空状态，直接复用 404，不增加单独的状态端点。
    if ((error as { status?: number }).status === 404) return null;
    throw error;
  }
}

function BackfillSkeleton() {
  return (
    <div aria-label="历史补处理状态加载中" className="grid grid-cols-4 gap-px bg-[var(--line)] max-lg:grid-cols-2">
      {[0, 1, 2, 3].map((item) => (
        <div className="min-h-20 bg-[var(--surface)] p-3" key={item}>
          <div className="h-3 w-16 rounded bg-[var(--surface-strong)]" />
          <div className="mt-3 h-5 w-20 rounded bg-[var(--surface-soft)]" />
        </div>
      ))}
    </div>
  );
}

function BackfillMetric({ label, value, tone = "neutral" }: { label: string; value: number; tone?: "neutral" | "warn" | "danger" }) {
  const valueClass = tone === "danger" ? "text-[var(--danger)]" : tone === "warn" ? "text-[var(--warn)]" : "";
  return (
    <div className="min-h-20 p-3">
      <div className="text-xs text-[var(--muted)]">{label}</div>
      <strong className={`mt-2 block font-mono text-lg font-semibold ${valueClass}`}>{value}</strong>
    </div>
  );
}

function BackfillDetail({ label, value }: { label: string; value: string }) {
  return (
    <div className="grid grid-cols-[92px_minmax(0,1fr)] gap-3">
      <span className="text-[var(--muted)]">{label}</span>
      <span className="min-w-0 break-words text-[var(--muted-strong)]">{value}</span>
    </div>
  );
}

function BackfillStageProgressSection({ stages }: { stages: StockV2NewsContextBackfillStageProgress[] }) {
  const aggregationStages = stages.filter((stage) => ["hourly", "four_hour", "daily"].includes(stage.phase));
  const completionStages = stages.filter((stage) => !["hourly", "four_hour", "daily"].includes(stage.phase));
  return (
    <section aria-label="历史补处理分阶段进度" className="border-b border-[var(--line)] px-4 py-3">
      <div className="mb-3">
        <h3 className="m-0 text-xs font-semibold text-[var(--text)]">分阶段进度</h3>
        <p className="mt-1 mb-0 text-xs text-[var(--muted)]">
          原始新闻覆盖完成后，还会继续执行四小时、日级归纳和最终检查。
        </p>
      </div>
      <div className="grid grid-cols-[minmax(0,1.3fr)_minmax(300px,1fr)] gap-5 max-lg:grid-cols-1">
        <BackfillStageGroup stages={aggregationStages} title="分层归纳" />
        <BackfillStageGroup stages={completionStages} title="收尾检查" />
      </div>
    </section>
  );
}

function BackfillStageGroup({
  stages,
  title,
}: {
  stages: StockV2NewsContextBackfillStageProgress[];
  title: string;
}) {
  return (
    <div>
      <h4 className="m-0 border-b border-[var(--line)] pb-2 text-[11px] font-medium text-[var(--muted)]">{title}</h4>
      <div>
        {stages.map((stage) => <BackfillStageRow key={stage.phase} stage={stage} />)}
      </div>
    </div>
  );
}

function BackfillStageRow({ stage }: { stage: StockV2NewsContextBackfillStageProgress }) {
  const active = stage.status === "running" || stage.status === "queued" || stage.status === "paused";
  const completedWindows = Math.max(0, stage.completedWindowCount || 0);
  const totalWindows = Math.max(0, stage.totalWindowCount || 0);
  const processedItems = Math.max(0, stage.processedItemCount || 0);
  const totalItems = Math.max(0, stage.totalItemCount || 0);
  const pendingItems = Math.max(0, stage.pendingItemCount || 0);
  const summary = totalWindows > 0
    ? `${completedWindows} / ${totalWindows} 个窗口`
    : stage.phase === "finalizing" && totalItems > 0
      ? `${processedItems} / ${totalItems} 个每日输出`
      : backfillStageDescription(stage.phase, stage.status);
  const detail = stage.currentWindowStart || stage.currentWindowEnd
    ? [
        `${formatNewsContextTime(stage.currentWindowStart)} 至 ${formatNewsContextTime(stage.currentWindowEnd)}`,
        totalItems > 0 ? `${processedItems} / ${totalItems} 个输入，待处理 ${pendingItems}` : "",
        backfillRunPhaseLabel(stage.currentRunPhase),
      ].filter(Boolean).join("，")
    : totalItems > 0 && totalWindows > 0
      ? `已处理 ${processedItems} / ${totalItems} 个输入`
      : "";
  return (
    <div
      aria-current={active ? "step" : undefined}
      className={`grid min-h-14 grid-cols-[minmax(0,1fr)_auto] items-center gap-3 border-b border-[var(--line)] py-2.5 last:border-b-0 ${active ? "-mx-2 bg-[var(--surface-soft)] px-2" : ""}`}
    >
      <div className="min-w-0">
        <div className="flex flex-wrap items-baseline gap-x-2 gap-y-1">
          <span className="text-xs font-medium text-[var(--text)]">{backfillStageLabel(stage.phase)}</span>
          <span className="font-mono text-[11px] text-[var(--muted-strong)]">{summary}</span>
        </div>
        {detail ? <div className="mt-1 break-words font-mono text-[11px] text-[var(--muted)]">{detail}</div> : null}
      </div>
      <Pill tone={backfillStageStatusTone(stage.status)}>{backfillStageStatusLabel(stage.status)}</Pill>
    </div>
  );
}

function backfillStageDescription(phase: string, status: string): string {
  const descriptions = {
    late_scan: { pending: "等待分层归纳完成", active: "正在扫描", completed: "扫描完成" },
    final_daily: { pending: "等待生成当前结论", active: "正在生成当前结论", completed: "当前结论已生成" },
    indexing: { pending: "等待更新可检索索引", active: "正在更新索引", completed: "索引已就绪" },
    final_review: { pending: "等待组合影响复核", active: "正在复核组合影响", completed: "组合影响已复核" },
    finalizing: { pending: "等待安全校验", active: "正在执行安全校验", completed: "安全校验完成" },
  } as Record<string, { pending: string; active: string; completed: string }>;
  const group = descriptions[phase];
  if (!group) return "等待执行";
  if (status === "completed") return group.completed;
  if (status === "failed") return "阶段执行失败";
  if (status === "running" || status === "queued" || status === "paused") return group.active;
  return group.pending;
}
