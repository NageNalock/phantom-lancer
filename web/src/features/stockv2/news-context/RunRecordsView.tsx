import { useEffect, useMemo, useState } from "react";
import { ArrowClockwise, Broom, CaretLeft, CaretRight, PlayCircle } from "@phosphor-icons/react";
import type { AppActions } from "../../../app/App";
import type { StockV2NewsContextRun, StockV2NewsContextRunListResponse } from "../../../app/types";
import { friendlyError } from "../../../api/client";
import { Button, Drawer, EmptyState, Notice, Pill, useDangerConfirm } from "../../../components/ui";
import {
  coverageStatusLabel,
  coverageStatusTone,
  formatNewsContextBytes,
  formatNewsContextTime,
  runStatusLabel,
  runStatusTone,
  windowTypeLabel,
} from "./model";

type RunKind = "aggregation" | "cleanup";
type RunStatusFilter = "all" | "pending" | "running" | "waiting_review" | "partial" | "completed" | "failed";

const PAGE_SIZE = 20;

export function RunRecordsView({
  actions,
  kind,
  refreshKey,
  onChanged,
}: {
  actions: AppActions;
  kind: RunKind;
  refreshKey: number;
  onChanged: () => void;
}) {
  const [items, setItems] = useState<StockV2NewsContextRun[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [status, setStatus] = useState<RunStatusFilter>("all");
  const [windowType, setWindowType] = useState("hourly");
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [selected, setSelected] = useState<StockV2NewsContextRun | null>(null);
  const { confirmDanger, dangerConfirmDialog } = useDangerConfirm();

  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE));
  const hasRunning = items.some((item) => item.status === "running" || item.status === "pending" || item.status === "queued");

  async function load(showLoading = false) {
    if (showLoading && items.length === 0) setLoading(true);
    else setRefreshing(true);
    setError(null);
    try {
      const params = new URLSearchParams({
        kind,
        limit: String(PAGE_SIZE),
        offset: String((page - 1) * PAGE_SIZE),
      });
      if (status !== "all") params.set("status", status);
      const result = await actions.api<StockV2NewsContextRunListResponse>(
        `/api/stockv2/news-context/runs?${params.toString()}`,
      );
      setItems(result.items || []);
      setTotal(result.total ?? result.items?.length ?? 0);
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
  }, [kind, page, status, refreshKey]);

  useEffect(() => {
    if ((kind === "aggregation" && status === "partial") || (kind === "cleanup" && status === "waiting_review")) setStatus("all");
  }, [kind, status]);

  useEffect(() => {
    if (!hasRunning) return;
    const timer = window.setInterval(() => void load(), 5000);
    return () => window.clearInterval(timer);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [hasRunning, kind, page, status]);

  async function createAggregationRun() {
    setBusy("create");
    try {
      await actions.api<StockV2NewsContextRun>("/api/stockv2/news-context/runs", {
        method: "POST",
        body: { windowType },
      });
      actions.setToast(`已触发${windowTypeLabel(windowType)}归纳`, "good");
      if (page !== 1) setPage(1);
      else await load();
      onChanged();
    } catch (createError) {
      actions.setToast(`触发归纳失败：${friendlyError(createError)}`, "danger");
    } finally {
      setBusy(null);
    }
  }

  async function createCleanupRun() {
    const confirmed = await confirmDanger({
      title: "执行安全清理",
      body: "只清理已经通过归纳、索引、CLI 检索、影响复核和引用检查的新闻资产。未通过安全门的内容继续保留。",
      impact: ["删除符合条件的普通新闻正文和旧检索资料", "保留主题、历史阶段和精简证据"],
      recovery: "运行中断后由后端从未完成阶段继续，不会跳过安全门。",
      confirmLabel: "执行清理",
    });
    if (!confirmed) return;
    setBusy("create");
    try {
      await actions.api<StockV2NewsContextRun>("/api/stockv2/news-context/cleanup-runs", { method: "POST" });
      actions.setToast("已触发安全清理", "good");
      if (page !== 1) setPage(1);
      else await load();
      onChanged();
    } catch (createError) {
      actions.setToast(`触发清理失败：${friendlyError(createError)}`, "danger");
    } finally {
      setBusy(null);
    }
  }

  async function retry(run: StockV2NewsContextRun) {
    setBusy(run.id);
    try {
      await actions.api<StockV2NewsContextRun>(
        `/api/stockv2/news-context/runs/${encodeURIComponent(run.id)}/retry`,
        { method: "POST" },
      );
      actions.setToast("已从失败阶段重新执行", "good");
      await load();
      onChanged();
    } catch (retryError) {
      actions.setToast(`重试失败：${friendlyError(retryError)}`, "danger");
    } finally {
      setBusy(null);
    }
  }

  const completedCount = useMemo(() => items.filter((item) => item.status === "completed").length, [items]);
  const failedCount = useMemo(() => items.filter((item) => item.status === "failed" || item.status === "partial").length, [items]);

  return (
    <div className="grid gap-4">
      <section className="rounded-lg border border-[var(--line)] bg-[var(--surface)]">
        <header className="flex flex-wrap items-start justify-between gap-3 border-b border-[var(--line)] p-4">
          <div>
            <h2 className="m-0 text-sm font-semibold">{kind === "aggregation" ? "归纳运行记录" : "新闻清理记录"}</h2>
            <p className="mt-1 mb-0 text-xs text-[var(--muted)]">
              {kind === "aggregation"
                ? "观察每小时、每四小时和每日的覆盖、主题变化与公开资料核实。"
                : "区分当前存量与历史处理量，追踪释放空间、保护原因和失败阶段。"}
            </p>
          </div>
          <div className="flex flex-wrap items-center justify-end gap-2">
            {kind === "aggregation" ? (
              <>
                <label>
                  <span className="sr-only">归纳周期</span>
                  <select aria-label="归纳周期" className="select h-9 text-xs" disabled={busy === "create"} onChange={(event) => setWindowType(event.target.value)} value={windowType}>
                    <option value="hourly">每小时</option>
                    <option value="four_hour">每四小时</option>
                    <option value="daily">每日</option>
                  </select>
                </label>
                <Button disabled={busy === "create"} onClick={() => void createAggregationRun()} tone="primary">
                  <PlayCircle size={14} />
                  {busy === "create" ? "触发中" : "立即归纳"}
                </Button>
              </>
            ) : (
              <Button disabled={busy === "create"} onClick={() => void createCleanupRun()} tone="danger">
                <Broom size={14} />
                {busy === "create" ? "触发中" : "执行安全清理"}
              </Button>
            )}
            <Button disabled={refreshing} onClick={() => void load()}>
              <ArrowClockwise size={14} />
              {refreshing ? "刷新中" : "刷新"}
            </Button>
          </div>
        </header>

        <div className="flex flex-wrap items-center justify-between gap-3 border-b border-[var(--line)] bg-[var(--surface-soft)] px-4 py-2 text-xs">
          <div className="flex flex-wrap items-center gap-3 text-[var(--muted)]">
            <span>共 {total} 次</span>
            <span>当前页完成 {completedCount}</span>
            <span className={failedCount ? "text-[var(--danger)]" : ""}>当前页异常 {failedCount}</span>
            {hasRunning ? <Pill tone="warn">自动刷新中</Pill> : null}
          </div>
          <label className="flex items-center gap-2">
            <span className="text-[var(--muted)]">状态</span>
            <select className="select h-8 text-xs" onChange={(event) => { setStatus(event.target.value as RunStatusFilter); setPage(1); }} value={status}>
              <option value="all">全部</option>
              <option value="pending">等待执行</option>
              <option value="running">执行中</option>
              {kind === "aggregation" ? <option value="waiting_review">等待复核</option> : null}
              {kind === "cleanup" ? <option value="partial">部分完成</option> : null}
              <option value="completed">已完成</option>
              <option value="failed">失败</option>
            </select>
          </label>
        </div>

        {error ? (
          <div className="p-4">
            <Notice tone="danger">
              <span className="flex flex-wrap items-center justify-between gap-2">
                <span>运行记录加载失败：{error}{items.length ? "。当前保留上次结果。" : ""}</span>
                <Button onClick={() => void load(true)}>重试</Button>
              </span>
            </Notice>
          </div>
        ) : null}

        {loading && items.length === 0 ? <RunListSkeleton /> : null}
        {!loading && items.length === 0 && !error ? (
          <div className="p-4">
            <EmptyState
              title={kind === "aggregation" ? "暂无归纳记录" : "暂无清理记录"}
              body={kind === "aggregation" ? "等待定时任务，或手动触发一次归纳。" : "只有通过安全门的新闻才会进入清理记录。"}
            />
          </div>
        ) : null}

        {items.length ? (
          <div className="divide-y divide-[var(--line)]">
            {items.map((run) => (
              <RunRow busy={busy === run.id} kind={kind} key={run.id} onOpen={() => setSelected(run)} onRetry={() => void retry(run)} run={run} />
            ))}
          </div>
        ) : null}

        {totalPages > 1 ? (
          <footer className="flex items-center justify-between border-t border-[var(--line)] px-4 py-3 text-xs text-[var(--muted)]">
            <Button disabled={page <= 1 || refreshing} onClick={() => setPage((value) => Math.max(1, value - 1))}>
              <CaretLeft size={14} />上一页
            </Button>
            <span>第 {page} / {totalPages} 页</span>
            <Button disabled={page >= totalPages || refreshing} onClick={() => setPage((value) => Math.min(totalPages, value + 1))}>
              下一页<CaretRight size={14} />
            </Button>
          </footer>
        ) : null}
      </section>

      {selected ? <RunDetailDrawer kind={kind} run={selected} onClose={() => setSelected(null)} /> : null}
      {dangerConfirmDialog}
    </div>
  );
}

function RunListSkeleton() {
  return (
    <div aria-label="运行记录加载中" className="divide-y divide-[var(--line)]">
      {[0, 1, 2, 3].map((item) => (
        <div className="grid grid-cols-[180px_minmax(0,1fr)_160px] gap-4 p-4" key={item}>
          <div className="h-4 rounded bg-[var(--surface-strong)]" />
          <div className="h-4 rounded bg-[var(--surface-soft)]" />
          <div className="h-4 rounded bg-[var(--surface-soft)]" />
        </div>
      ))}
    </div>
  );
}

function RunRow({
  busy,
  kind,
  onOpen,
  onRetry,
  run,
}: {
  busy: boolean;
  kind: RunKind;
  onOpen: () => void;
  onRetry: () => void;
  run: StockV2NewsContextRun;
}) {
  const processed = run.processedNewsCount ?? 0;
  const total = run.totalNewsCount ?? 0;
  return (
    <article className="grid grid-cols-[180px_minmax(0,1fr)_auto] items-center gap-4 p-4 max-lg:grid-cols-1">
      <div>
        <div className="flex flex-wrap items-center gap-2">
          <strong className="text-sm">{kind === "aggregation" ? windowTypeLabel(run.windowType) : "安全清理"}</strong>
          <Pill tone={runStatusTone(run.status)}>{runStatusLabel(run.status)}</Pill>
        </div>
        <div className="mt-1 text-xs text-[var(--muted)]">{formatNewsContextTime(run.startedAt || run.createdAt)}</div>
      </div>
      <div className="min-w-0">
        <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-[var(--muted-strong)]">
          <span>处理 {processed}{total ? ` / ${total}` : ""} 条</span>
          {kind === "aggregation" ? (
            <>
              <span>新建 {run.createdThemeCount ?? 0}</span>
              <span>更新 {run.updatedThemeCount ?? 0}</span>
              <span>冲突 {run.conflictThemeCount ?? 0}</span>
            </>
          ) : (
            <>
              <span>删除 {run.deletedNewsCount ?? 0}</span>
              <span>保留 {run.retainedNewsCount ?? 0}</span>
              <span>释放 {formatNewsContextBytes(run.releasedBytes)}</span>
            </>
          )}
        </div>
        <div className="mt-2 flex flex-wrap items-center gap-2">
          <Pill tone={coverageStatusTone(run.coverageStatus)}>{coverageStatusLabel(run.coverageStatus)}</Pill>
          {run.failedStage ? <span className="text-xs text-[var(--danger)]">失败阶段：{run.failedStage}</span> : null}
          {run.errorMessage ? <span className="max-w-xl truncate text-xs text-[var(--danger)]">{run.errorMessage}</span> : null}
        </div>
      </div>
      <div className="flex justify-end gap-2">
        <Button onClick={onOpen}>详情</Button>
        {run.retryable || run.status === "failed" || run.status === "partial" ? (
          <Button disabled={busy} onClick={onRetry}>{busy ? "重试中" : "从失败处重试"}</Button>
        ) : null}
      </div>
    </article>
  );
}

function RunDetailDrawer({ kind, run, onClose }: { kind: RunKind; run: StockV2NewsContextRun; onClose: () => void }) {
  // ponytail: 列表接口已经返回完整观测字段，详情直接复用当前记录，避免再造一个只读详情请求。
  const rows: Array<[string, string | number]> = [
    ["运行身份", run.id],
    ["运行类型", kind === "aggregation" ? windowTypeLabel(run.windowType) : "安全清理"],
    ["状态", runStatusLabel(run.status)],
    ["覆盖", coverageStatusLabel(run.coverageStatus)],
    ["时间范围", `${formatNewsContextTime(run.windowStart)} 至 ${formatNewsContextTime(run.windowEnd)}`],
    ["开始", formatNewsContextTime(run.startedAt || run.createdAt)],
    ["完成", formatNewsContextTime(run.finishedAt)],
    ["消息处理", `${run.processedNewsCount ?? 0}${run.totalNewsCount ? ` / ${run.totalNewsCount}` : ""}`],
  ];
  if (kind === "aggregation") {
    rows.push(
      ["新建主题", run.createdThemeCount ?? 0],
      ["更新主题", run.updatedThemeCount ?? 0],
      ["保持不变", run.unchangedThemeCount ?? 0],
      ["存在冲突", run.conflictThemeCount ?? 0],
      ["等待处理", run.pendingThemeCount ?? 0],
      ["实质变化", run.materialThemeCount ?? 0],
      ["公开资料核实", run.externalResearchStatus === "recorded" ? "已留痕" : run.externalResearchStatus === "not_required" ? "本次无需核实" : run.externalResearchStatus || "未记录"],
    );
  } else {
    rows.push(
      ["删除新闻", run.deletedNewsCount ?? 0],
      ["保留新闻", run.retainedNewsCount ?? 0],
      ["等待清理", run.pendingCleanupCount ?? 0],
      ["受保护", run.protectedNewsCount ?? 0],
      ["释放空间", formatNewsContextBytes(run.releasedBytes)],
    );
  }
  return (
    <Drawer onClose={onClose} subtitle="运行记录只展示摘要，不复制即将清理的新闻全文" title={kind === "aggregation" ? "归纳运行详情" : "清理运行详情"} width={620}>
      <div className="grid gap-4">
        <div className="divide-y divide-[var(--line)] rounded-lg border border-[var(--line)] bg-[var(--surface)]">
          {rows.map(([label, value]) => (
            <div className="grid grid-cols-[120px_minmax(0,1fr)] gap-3 px-3 py-2.5 text-sm" key={label}>
              <span className="text-xs text-[var(--muted)]">{label}</span>
              <span className={label === "运行身份" ? "break-all font-mono text-xs" : "break-words"}>{value}</span>
            </div>
          ))}
        </div>
        {run.errorMessage ? <Notice tone="danger"><strong>失败摘要：</strong>{run.errorMessage}</Notice> : null}
        {run.failedStage ? <Notice tone="warn"><strong>可恢复阶段：</strong>{run.failedStage}</Notice> : null}
      </div>
    </Drawer>
  );
}
