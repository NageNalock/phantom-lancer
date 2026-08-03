import { useEffect, useMemo, useState } from "react";
import { ArrowClockwise, Broom, CaretLeft, CaretRight, PlayCircle } from "@phosphor-icons/react";
import type { AppActions } from "../../../app/App";
import type {
  StockV2NewsContextRun,
  StockV2NewsContextRunListResponse,
  StockV2NewsContextSummary,
} from "../../../app/types";
import { friendlyError } from "../../../api/client";
import { Button, Drawer, EmptyState, Notice, Pill, useDangerConfirm } from "../../../components/ui";
import {
  backfillRunPhaseLabel,
  coverageStatusLabel,
  coverageStatusTone,
  formatNewsContextBytes,
  formatNewsContextTime,
  newsContextRunCoverage,
  runStatusLabel,
  runStatusTone,
  windowTypeLabel,
} from "./model";
import { NewsContextBackfillPanel } from "./NewsContextBackfillPanel";

type RunKind = "aggregation" | "cleanup";
type RunStatusFilter = "all" | "pending" | "running" | "waiting_review" | "partial" | "completed" | "failed";
type WindowTypeFilter = "all" | "hourly" | "four_hour" | "daily";

const PAGE_SIZE = 20;

export function RunRecordsView({
  actions,
  kind,
  refreshKey,
  summary,
  onChanged,
}: {
  actions: AppActions;
  kind: RunKind;
  refreshKey: number;
  summary: StockV2NewsContextSummary | null;
  onChanged: () => void;
}) {
  const [items, setItems] = useState<StockV2NewsContextRun[]>([]);
  const [total, setTotal] = useState(0);
  const [failedItems, setFailedItems] = useState<StockV2NewsContextRun[]>([]);
  const [failedTotal, setFailedTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [status, setStatus] = useState<RunStatusFilter>("all");
  const [windowType, setWindowType] = useState<WindowTypeFilter>("all");
  const [triggerWindowType, setTriggerWindowType] = useState("four_hour");
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [selected, setSelected] = useState<StockV2NewsContextRun | null>(null);
  const { confirmDanger, dangerConfirmDialog } = useDangerConfirm();

  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE));
  const hasRunning = [...items, ...failedItems].some((item) =>
    item.status === "running" || item.status === "pending" || item.status === "queued" ||
    (item.status === "waiting_review" && item.reviewStatus !== "failed") || Boolean(item.nextRetryAt),
  );
  const cleanupGate = kind === "cleanup" ? summary?.cleanupGate : undefined;
  const cleanupBlocked = Boolean(cleanupGate?.blocked);

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
      if (kind === "aggregation" && windowType !== "all") params.set("windowType", windowType);
      const [result, failures] = await Promise.all([
        actions.api<StockV2NewsContextRunListResponse>(`/api/stockv2/news-context/runs?${params.toString()}`),
        kind === "aggregation"
          ? actions.api<StockV2NewsContextRunListResponse>("/api/stockv2/news-context/runs?kind=aggregation&status=failed&limit=5&offset=0")
          : Promise.resolve<StockV2NewsContextRunListResponse>({ items: [], total: 0 }),
      ]);
      setItems(result.items || []);
      setTotal(result.total ?? result.items?.length ?? 0);
      setFailedItems(failures?.items || []);
      setFailedTotal(failures?.total ?? failures?.items?.length ?? 0);
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
  }, [kind, page, status, windowType, refreshKey]);

  useEffect(() => {
    if ((kind === "aggregation" && status === "partial") || (kind === "cleanup" && status === "waiting_review")) setStatus("all");
  }, [kind, status]);

  useEffect(() => {
    if (!hasRunning) return;
    const timer = window.setInterval(() => void load(), 5000);
    return () => window.clearInterval(timer);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [hasRunning, kind, page, status, windowType]);

  async function createAggregationRun() {
    setBusy("create");
    try {
      await actions.api<StockV2NewsContextRun>("/api/stockv2/news-context/runs", {
        method: "POST",
        csrf: actions.csrf,
        body: { windowType: triggerWindowType },
      });
      actions.setToast(`已触发${windowTypeLabel(triggerWindowType)}归纳`, "good");
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
      await actions.api<StockV2NewsContextRun>("/api/stockv2/news-context/cleanup-runs", { method: "POST", csrf: actions.csrf });
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
        { method: "POST", csrf: actions.csrf },
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
      {kind === "aggregation" ? (
        <NewsContextBackfillPanel actions={actions} onChanged={onChanged} refreshKey={refreshKey} />
      ) : null}
      <section className="rounded-lg border border-[var(--line)] bg-[var(--surface)]">
        <header className="flex flex-wrap items-start justify-between gap-3 border-b border-[var(--line)] p-4">
          <div>
            <h2 className="m-0 text-sm font-semibold">{kind === "aggregation" ? "归纳运行记录" : "新闻清理记录"}</h2>
            <p className="mt-1 mb-0 text-xs text-[var(--muted)]">
              {kind === "aggregation"
                ? "观察小时检查点、四小时模型归纳、日级增量物化与公开资料核实。"
                : "区分当前存量与历史处理量，追踪移除内容量、保护原因和失败阶段。"}
            </p>
          </div>
          <div className="flex flex-wrap items-center justify-end gap-2">
            {kind === "aggregation" ? (
              <>
                <label>
                  <span className="sr-only">立即执行周期</span>
                  <select aria-label="立即执行周期" className="select h-9 text-xs" disabled={busy === "create"} onChange={(event) => setTriggerWindowType(event.target.value)} value={triggerWindowType}>
                    <option value="hourly">小时检查点</option>
                    <option value="four_hour">四小时模型归纳</option>
                    <option value="daily">日级增量物化</option>
                  </select>
                </label>
                <Button disabled={busy === "create"} onClick={() => void createAggregationRun()} tone="primary">
                  <PlayCircle size={14} />
                  {busy === "create" ? "触发中" : "立即执行"}
                </Button>
              </>
            ) : (
              <Button disabled={busy === "create" || cleanupBlocked} onClick={() => void createCleanupRun()} tone="danger">
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

        {kind === "cleanup" && cleanupGate ? (
          <div className="flex flex-wrap items-start justify-between gap-3 border-b border-[var(--line)] px-4 py-3 text-xs">
            <div className="grid gap-1">
              <span className="flex items-center gap-2">
                <Pill tone={cleanupBlocked ? "warn" : "good"}>{cleanupBlocked ? "清理被阻塞" : "清理可运行"}</Pill>
                <span>{cleanupBlocked
                  ? cleanupGate.reason || "清理截止点前仍有未完成归纳的消息"
                  : "清理截止点前没有未归纳消息，可以继续执行逐条安全检查。"}</span>
              </span>
              <span className="text-[var(--muted)]">
                截止时间 <span className="font-mono">{formatNewsContextTime(cleanupGate.cutoff)}</span>
                {" · "}截止点前欠账 {cleanupGate.backlogCount ?? 0} 条
                {" · "}待处理 {cleanupGate.pendingCount ?? 0}
                {" · "}延后 {cleanupGate.deferredCount ?? 0}
                {" · "}处理中 {cleanupGate.claimedCount ?? 0}
              </span>
            </div>
            {cleanupGate.activeBackfill ? <Pill tone="warn">历史补处理运行中</Pill> : null}
          </div>
        ) : null}

        <div className="flex flex-wrap items-center justify-between gap-3 border-b border-[var(--line)] bg-[var(--surface-soft)] px-4 py-2 text-xs">
          <div className="flex flex-wrap items-center gap-3 text-[var(--muted)]">
            <span>共 {total} 次</span>
            <span>当前页完成 {completedCount}</span>
            <span className={failedCount ? "text-[var(--danger)]" : ""}>当前页异常 {failedCount}</span>
            {hasRunning ? <Pill tone="warn">自动刷新中</Pill> : null}
          </div>
          <div className="flex flex-wrap items-center gap-3">
            {kind === "aggregation" ? (
              <label className="flex items-center gap-2">
                <span className="text-[var(--muted)]">周期</span>
                <select className="select h-8 text-xs" onChange={(event) => { setWindowType(event.target.value as WindowTypeFilter); setPage(1); }} value={windowType}>
                  <option value="all">全部</option>
                  <option value="hourly">小时检查点</option>
                  <option value="four_hour">四小时模型归纳</option>
                  <option value="daily">日级增量物化</option>
                </select>
              </label>
            ) : null}
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
        </div>

        {kind === "aggregation" && failedItems.length ? (
          <LatestFailureNotice
            busy={busy === failedItems[0].id}
            failedCount={failedTotal}
            onOpen={() => setSelected(failedItems[0])}
            onRetry={() => void retry(failedItems[0])}
            run={failedItems[0]}
          />
        ) : null}

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
  const coverage = newsContextRunCoverage(run);
  const status = run.reviewStatus === "failed" ? "failed" : run.status;
  return (
    <article className="grid grid-cols-[180px_minmax(0,1fr)_auto] items-center gap-4 p-4 max-lg:grid-cols-1">
      <div>
        <div className="flex flex-wrap items-center gap-2">
          <strong className="text-sm">{kind === "aggregation" ? windowTypeLabel(run.windowType) : "安全清理"}</strong>
          <Pill tone={runStatusTone(status)}>{runStatusLabel(status)}</Pill>
          {run.phase ? <Pill tone="neutral">{backfillRunPhaseLabel(run.phase)}</Pill> : null}
        </div>
        <div className="mt-1 text-xs text-[var(--muted)]">{formatNewsContextTime(run.startedAt || run.createdAt)}</div>
      </div>
      <div className="min-w-0">
        <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-[var(--muted-strong)]">
          {kind === "aggregation" && run.windowType === "hourly" ? (
            <span>确定性检查点，不调用模型</span>
          ) : kind === "aggregation" && run.windowType === "daily" ? (
            <>
              <span>已物化主题 {run.processedNewsCount ?? 0}</span>
              <span>待物化 {run.pendingThemeCount ?? 0}</span>
              <span>实质变化沿用四小时结论</span>
            </>
          ) : kind === "aggregation" ? (
            coverage.empty ? (
              <span>空窗口，无需处理</span>
            ) : (
              <>
                <span>总新闻 {coverage.total}</span>
                <span>覆盖 {coverage.covered}</span>
                <span>噪音 {coverage.noise}</span>
                <span>延后 {coverage.deferred}</span>
                <span>遗漏/等待 {coverage.waiting}</span>
                <span>覆盖率 {coverage.percent}%</span>
                <span>新建 {run.createdThemeCount ?? 0}</span>
                <span>更新 {run.updatedThemeCount ?? 0}</span>
                <span>冲突 {run.conflictThemeCount ?? 0}</span>
              </>
            )
          ) : (
            <>
              <span>处理 {run.processedNewsCount ?? 0}{run.totalNewsCount ? ` / ${run.totalNewsCount}` : ""} 条</span>
              <span>符合条件 {run.eligibleCount ?? "未知"}</span>
              <span>删除 {run.deletedNewsCount ?? 0}</span>
              <span>保留 {run.retainedNewsCount ?? 0}</span>
              <span className={typeof run.failedCount === "number" && run.failedCount > 0 ? "text-[var(--danger)]" : ""}>失败 {run.failedCount ?? "未知"}</span>
              <span>移除内容 {formatNewsContextBytes(run.releasedBytes)}</span>
            </>
          )}
        </div>
        <div className="mt-2 flex flex-wrap items-center gap-2">
          <Pill tone={coverage.empty ? "neutral" : coverageStatusTone(run.coverageStatus)}>
            {coverage.empty ? "无需处理" : coverageStatusLabel(run.coverageStatus)}
          </Pill>
          {run.failedStage ? <span className="text-xs text-[var(--danger)]">失败阶段：{run.failedStage}</span> : null}
          {run.nextRetryAt ? <span className="text-xs text-[var(--warn)]">下次自动重试：{formatNewsContextTime(run.nextRetryAt)}</span> : null}
          {run.autoRetryExhausted ? <span className="text-xs text-[var(--danger)]">不会再自动重试</span> : null}
          {(run.retryCount ?? 0) > 0 ? <span className="font-mono text-xs text-[var(--muted)]">重试 {run.retryCount} / {run.retryLimit ?? 2}</span> : null}
          {(run.reviewCoverageCount ?? 0) > 1 ? <span className="text-xs text-[var(--muted-strong)]">合并复核覆盖 {run.reviewCoverageCount} 个窗口</span> : null}
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
  const coverage = newsContextRunCoverage(run);
  const status = run.reviewStatus === "failed" ? "failed" : run.status;
  const rows: Array<[string, string | number]> = [
    ["运行身份", run.id],
    ["运行类型", kind === "aggregation" ? windowTypeLabel(run.windowType) : "安全清理"],
    ["状态", runStatusLabel(status)],
    ["执行阶段", backfillRunPhaseLabel(run.phase) || "-"],
    ["覆盖", coverageStatusLabel(run.coverageStatus)],
    ["时间范围", `${formatNewsContextTime(run.windowStart)} 至 ${formatNewsContextTime(run.windowEnd)}`],
    ["开始", formatNewsContextTime(run.startedAt || run.createdAt)],
    ["完成", formatNewsContextTime(run.finishedAt)],
    ["自动重试", `${run.retryCount ?? 0} / ${run.retryLimit ?? 2}`],
  ];
  if (run.nextRetryAt) rows.push(["下次自动重试", formatNewsContextTime(run.nextRetryAt)]);
  if (run.autoRetryExhausted) rows.push(["自动重试状态", "已停止，保留手动重试入口"]);
  if ((run.reviewCoverageCount ?? 0) > 1) rows.push(["复核覆盖", `本次组合哨兵合并覆盖 ${run.reviewCoverageCount} 个归纳窗口`]);
  if (kind === "aggregation") {
    if (run.windowType === "hourly") {
      rows.push(["处理方式", "确定性检查点，不调用模型"]);
    } else if (run.windowType === "daily") {
      rows.push(
        ["处理方式", "只增量物化四小时窗口中发生变化的稳定主题"],
        ["已物化主题", run.processedNewsCount ?? 0],
        ["待物化主题", run.pendingThemeCount ?? 0],
      );
    } else if (coverage.empty) {
      rows.push(["总新闻", 0], ["处理结果", "空窗口，无需处理"]);
    } else {
      rows.push(
        ["总新闻", coverage.total],
        ["已覆盖", coverage.covered],
        ["重复噪音", coverage.noise],
        ["受保护延后", coverage.deferred],
        ["遗漏或等待", coverage.waiting],
        ["覆盖率", `${coverage.percent}%`],
        ["新建主题", run.createdThemeCount ?? 0],
        ["更新主题", run.updatedThemeCount ?? 0],
        ["保持不变", run.unchangedThemeCount ?? 0],
        ["存在冲突", run.conflictThemeCount ?? 0],
        ["实质变化", run.materialThemeCount ?? 0],
        ["公开资料核实", run.externalResearchStatus === "recorded" ? "已留痕" : run.externalResearchStatus === "not_required" ? "本次无需核实" : run.externalResearchStatus || "未记录"],
      );
    }
  } else {
    rows.push(
      ["消息处理", `${run.processedNewsCount ?? 0}${run.totalNewsCount ? ` / ${run.totalNewsCount}` : ""}`],
      ["符合清理条件", run.eligibleCount ?? "未知"],
      ["删除新闻", run.deletedNewsCount ?? 0],
      ["保留新闻", run.retainedNewsCount ?? 0],
      ["受保护", run.protectedNewsCount ?? 0],
      ["处理失败", run.failedCount ?? "未知"],
      ["移除内容量", formatNewsContextBytes(run.releasedBytes)],
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

function LatestFailureNotice({
  busy,
  failedCount,
  onOpen,
  onRetry,
  run,
}: {
  busy: boolean;
  failedCount: number;
  onOpen: () => void;
  onRetry: () => void;
  run: StockV2NewsContextRun;
}) {
  const waitingForRetry = Boolean(run.nextRetryAt) && !run.autoRetryExhausted;
  return (
    <div aria-live="polite" className="border-b border-[var(--line)] p-4" role="status">
      <Notice tone={waitingForRetry ? "warn" : "danger"}>
        <span className="flex flex-wrap items-center justify-between gap-3">
          <span className="min-w-0">
            <strong>{windowTypeLabel(run.windowType)}{waitingForRetry ? "失败，等待自动重试" : "最终失败"}</strong>
            <span className="mt-1 block text-xs">
              {formatNewsContextTime(run.windowStart)} 至 {formatNewsContextTime(run.windowEnd)}
              {`，已自动重试 ${run.retryCount ?? 0} / ${run.retryLimit ?? 2} 次`}
              {run.nextRetryAt ? `，下次 ${formatNewsContextTime(run.nextRetryAt)}` : ""}
              {failedCount > 1 ? `，另有 ${failedCount - 1} 条失败记录` : ""}
            </span>
            {run.errorMessage ? <span className="mt-1 block max-w-4xl break-words text-xs">{run.errorMessage}</span> : null}
          </span>
          <span className="flex shrink-0 gap-2">
            <Button onClick={onOpen}>查看详情</Button>
            <Button disabled={busy} onClick={onRetry}>{busy ? "重试中" : "立即重试"}</Button>
          </span>
        </span>
      </Notice>
    </div>
  );
}
