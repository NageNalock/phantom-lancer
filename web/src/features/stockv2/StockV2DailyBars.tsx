import { useEffect, useMemo, useRef, useState } from "react";
import { ChartLine, CheckCircle, PlayCircle, RewindCircle, X, XCircle } from "@phosphor-icons/react";
import type { AppActions } from "../../app/App";
import type {
  AppData,
  DailyBarAdjusted,
  DailyBarRange,
  StockV2DailyBarJob,
  StockV2DailyBarsJobRequest,
  StockV2DailyBarsQuality,
} from "../../app/types";
import { friendlyError } from "../../api/client";
import { Button, CollapsibleSection, Field, Notice, Pill } from "../../components/ui";
import {
  stockV2AdjustedLabel,
  stockV2DailyBarJobStatusLabel,
  stockV2DailyBarJobStatusTone,
  stockV2DailyBarJobTypeLabel,
  stockV2RangeLabel,
} from "../../domain/labels";
import { StockV2Monitor } from "./StockV2Monitor";
import { StockV2NewsWorkbench } from "./StockV2NewsWorkbench";
import { formatCompactMeaningfulTime as formatCompactTime, hasMeaningfulTime } from "./time";

type RunAction = (label: string, fn: () => Promise<void>) => Promise<void>;
type MarketView = "monitor" | "news";

const RANGES: DailyBarRange[] = ["6m", "1y", "3y", "5y"];
const ADJUSTEDS: DailyBarAdjusted[] = ["none", "qfq", "hfq"];
const JOB_PAGE_SIZE = 10;

interface JobsResponse {
  items?: StockV2DailyBarJob[];
  running?: StockV2DailyBarJob[];
  total?: number;
  limit?: number;
  offset?: number;
}

export function StockV2DailyBars({ actions }: { actions: AppActions; data: AppData; runAction: RunAction }) {
  const [marketView, setMarketView] = useState<MarketView>("monitor");

  return (
    <div className="grid gap-4">
      <div className="flex flex-wrap items-center gap-2 border-b border-[var(--line)] pb-3">
        {[
          { id: "monitor" as const, label: "监控任务" },
          { id: "news" as const, label: "消息面" },
        ].map((tab) => (
          <button
            className={`rounded-md border px-3 py-1.5 text-sm ${
              marketView === tab.id
                ? "border-[var(--accent)] bg-[var(--surface-strong)] text-[var(--text)]"
                : "border-[var(--line)] text-[var(--muted-strong)] hover:bg-[var(--surface-soft)]"
            }`}
            key={tab.id}
            onClick={() => setMarketView(tab.id)}
            type="button"
          >
            {tab.label}
          </button>
        ))}
      </div>

      {marketView === "monitor" ? <StockV2Monitor actions={actions} /> : null}
      {marketView === "news" ? <StockV2NewsWorkbench actions={actions} /> : null}
    </div>
  );
}

export function StockV2DailyBarsMaintenance({ actions, data, runAction }: { actions: AppActions; data: AppData; runAction: RunAction }) {
  const settings = data.stockv2.settings;
  const [jobs, setJobs] = useState<StockV2DailyBarJob[]>([]);
  const [runningJobs, setRunningJobs] = useState<StockV2DailyBarJob[]>([]);
  const [jobTotal, setJobTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [loading, setLoading] = useState(false);
  const [drawerOpen, setDrawerOpen] = useState(false);
  const [symbol, setSymbol] = useState("");
  const [range, setRange] = useState<DailyBarRange>("1y");
  const [adjusted, setAdjusted] = useState<DailyBarAdjusted>("none");
  const [expandedJobId, setExpandedJobId] = useState<string | null>(null);
  const [symbolCheck, setSymbolCheck] = useState<StockV2DailyBarsQuality | null>(null);
  const pollRef = useRef<number | null>(null);

  const loadJobs = async (nextPage = page, showLoading = false) => {
    if (showLoading) setLoading(true);
    try {
      const safePage = Math.max(1, nextPage);
      const offset = (safePage - 1) * JOB_PAGE_SIZE;
      const r = await actions.api<JobsResponse>(
        `/api/stockv2/history/daily/jobs?limit=${JOB_PAGE_SIZE}&offset=${offset}`,
      );
      const total = r.total ?? r.items?.length ?? 0;
      setJobs(r.items ?? []);
      setRunningJobs(r.running ?? []);
      setJobTotal(total);
      if (total > 0 && offset >= total) {
        setPage(Math.max(1, Math.ceil(total / JOB_PAGE_SIZE)));
      }
    } catch (e) {
      actions.setToast(`加载日 K 任务失败：${friendlyError(e)}`, "danger");
    } finally {
      if (showLoading) setLoading(false);
    }
  };

  useEffect(() => {
    void loadJobs(page, true);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [page]);

  const runningJob = useMemo(
    () => runningJobs[0] || jobs.find((j) => j.status === "running"),
    [jobs, runningJobs],
  );
  const hasRunning = Boolean(runningJob);

  useEffect(() => {
    if (!hasRunning) {
      if (pollRef.current !== null) {
        window.clearInterval(pollRef.current);
        pollRef.current = null;
      }
      return;
    }
    if (pollRef.current === null) {
      pollRef.current = window.setInterval(() => {
        void loadJobs(page, false);
      }, 2000);
    }
    return () => {
      if (pollRef.current !== null) {
        window.clearInterval(pollRef.current);
        pollRef.current = null;
      }
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [hasRunning, page]);

  const triggerRun = async (mode: "symbol" | "hot" | "universe_incremental") => {
    const labelMap: Record<string, string> = {
      symbol: `补拉单只 ${symbol}`,
      hot: "拉取持仓热集合日 K",
      universe_incremental: "全市场最近交易日增量",
    };
    await runAction(labelMap[mode] || mode, async () => {
      const req: StockV2DailyBarsJobRequest = {
        mode,
        range,
        adjusted,
        triggerType: "manual",
        triggerSource: "web",
      };
      if (mode === "symbol") {
        if (!symbol.trim()) throw new Error("请输入标的代码");
        req.symbol = symbol.trim();
      }
      await actions.api<StockV2DailyBarJob>("/api/stockv2/history/daily/jobs/run", {
        method: "POST",
        body: req,
        csrf: actions.csrf,
      });
      setDrawerOpen(false);
      setPage(1);
      await loadJobs(1, false);
    });
  };

  const checkSymbolQuality = async () => {
    if (!symbol.trim()) {
      setSymbolCheck(null);
      return;
    }
    try {
      const q = await actions.api<StockV2DailyBarsQuality>(
        `/api/stockv2/history/daily/quality?symbol=${encodeURIComponent(symbol.trim())}&adjusted=${adjusted}`,
      );
      setSymbolCheck(q);
    } catch (e) {
      actions.setToast(`质量检查失败：${friendlyError(e)}`, "danger");
      setSymbolCheck(null);
    }
  };

  useEffect(() => {
    setSymbolCheck(null);
  }, [symbol, adjusted]);

  const totalPages = Math.max(1, Math.ceil(jobTotal / JOB_PAGE_SIZE));
  const pageNumbers = useMemo(() => paginationWindow(page, totalPages), [page, totalPages]);
  const visibleJobs = useMemo(() => mergeRunningIntoPage(runningJobs, jobs), [jobs, runningJobs]);
  const historySubtitle = runningJob
    ? `运行中 · 已处理 ${runningJob.processedCount}/${runningJob.totalCount || "-"} · 共 ${jobTotal} 条`
    : `共 ${jobTotal} 条日 K 任务，运行中任务会固定显示在历史顶部`;

  return (
    <div className="grid gap-4">
      <CollapsibleSection
        title={
          <span className="flex items-center gap-2">
            <ChartLine size={16} style={{ color: "var(--accent)" }} />
            手动日 K 抓取历史
          </span>
        }
        subtitle={`手动补拉和临时全市场增量记录 · ${historySubtitle}`}
        defaultOpen
      >
        <div className="flex flex-wrap justify-end gap-2">
          <Button onClick={() => void loadJobs(page, true)} disabled={loading}>
            {loading ? "刷新中" : "刷新"}
          </Button>
          <Button onClick={() => setDrawerOpen(true)} tone="primary">
            <PlayCircle size={14} className="mr-1.5" />
            手动抓取
          </Button>
        </div>

        {runningJob ? (
          <Notice tone="warn">
            <span className="text-xs">
              当前有日 K 任务正在执行：{stockV2DailyBarJobTypeLabel(runningJob)}
              {runningJob.totalCount ? `，已处理 ${runningJob.processedCount}/${runningJob.totalCount}` : "，正在初始化任务范围"}。
              历史列表会自动刷新。
            </span>
          </Notice>
        ) : null}

        {visibleJobs.length === 0 ? (
          <div className="rounded-lg border border-dashed border-[var(--line)] bg-[var(--surface-soft)] p-6 text-center text-sm text-[var(--muted)]">
            暂无日 K 任务记录。点击“手动抓取”可以立即创建一次任务；统一维护调度在维护配置里。
          </div>
        ) : (
          <div className="grid gap-2">
            {visibleJobs.map((job) => (
              <DailyBarJobCard
                expanded={expandedJobId === job.id}
                fixedRunning={job.status === "running" && !jobs.some((item) => item.id === job.id)}
                job={job}
                key={job.id}
                onToggle={() => setExpandedJobId(expandedJobId === job.id ? null : job.id)}
              />
            ))}
          </div>
        )}

        {jobTotal > JOB_PAGE_SIZE ? (
          <div className="mt-4 flex flex-wrap items-center justify-between gap-3 border-t border-[var(--line)] pt-3 text-xs">
            <span className="text-[var(--muted)]">
              第 {page} / {totalPages} 页
            </span>
            <div className="flex flex-wrap items-center gap-1.5">
              <Button disabled={page <= 1} onClick={() => setPage((current) => Math.max(1, current - 1))}>
                上一页
              </Button>
              {pageNumbers.map((item, index) =>
                item === "ellipsis" ? (
                  <span className="px-2 text-[var(--muted)]" key={`ellipsis-${index}`}>...</span>
                ) : (
                  <Button
                    className={item === page ? "border-[var(--accent)] text-[var(--accent)]" : ""}
                    key={item}
                    onClick={() => setPage(item)}
                  >
                    {item}
                  </Button>
                ),
              )}
              <Button disabled={page >= totalPages} onClick={() => setPage((current) => Math.min(totalPages, current + 1))}>
                下一页
              </Button>
              <select
                aria-label="选择页码"
                className="select h-9 w-24 text-xs"
                onChange={(event) => setPage(Number(event.target.value))}
                value={page}
              >
                {Array.from({ length: totalPages }, (_, idx) => idx + 1).map((item) => (
                  <option key={item} value={item}>
                    第 {item} 页
                  </option>
                ))}
              </select>
            </div>
          </div>
        ) : null}
      </CollapsibleSection>

      {drawerOpen ? (
        <DailyBarsTriggerDrawer
          adjusted={adjusted}
          hasRunning={hasRunning}
          onAdjustedChange={setAdjusted}
          onCheckSymbol={() => void checkSymbolQuality()}
          onClose={() => setDrawerOpen(false)}
          onRangeChange={setRange}
          onSymbolChange={setSymbol}
          onTrigger={(mode) => void triggerRun(mode)}
          range={range}
          unifiedMaintenanceEnabled={!!settings?.autoUpdateEnabled}
          symbol={symbol}
          symbolCheck={symbolCheck}
        />
      ) : null}
    </div>
  );
}

function DailyBarsTriggerDrawer({
  adjusted,
  hasRunning,
  onAdjustedChange,
  onCheckSymbol,
  onClose,
  onRangeChange,
  onSymbolChange,
  onTrigger,
  range,
  unifiedMaintenanceEnabled,
  symbol,
  symbolCheck,
}: {
  adjusted: DailyBarAdjusted;
  hasRunning: boolean;
  onAdjustedChange: (value: DailyBarAdjusted) => void;
  onCheckSymbol: () => void;
  onClose: () => void;
  onRangeChange: (value: DailyBarRange) => void;
  onSymbolChange: (value: string) => void;
  onTrigger: (mode: "symbol" | "hot" | "universe_incremental") => void;
  range: DailyBarRange;
  unifiedMaintenanceEnabled: boolean;
  symbol: string;
  symbolCheck: StockV2DailyBarsQuality | null;
}) {
  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [onClose]);

  return (
    <div className="fixed inset-0 z-50" role="presentation" onClick={onClose}>
      <div className="absolute inset-0 bg-[rgba(16,18,22,0.56)]" />
      <aside
        aria-label="手动触发日 K 抓取"
        aria-modal="true"
        className="absolute right-0 top-0 flex h-full w-[min(620px,100vw)] flex-col border-l border-[var(--line)] bg-[var(--surface)] shadow-[var(--shadow)]"
        onClick={(event) => event.stopPropagation()}
        role="dialog"
      >
        <header className="flex items-start gap-3 border-b border-[var(--line)] p-4">
          <div className="min-w-0 flex-1">
            <h3 className="m-0 text-base font-semibold">手动触发日 K 抓取</h3>
            <p className="muted mt-1 mb-0 text-xs">立即创建一次任务；任务进度和结果会回到历史列表。</p>
          </div>
          <Button aria-label="关闭" className="px-2 py-1 text-xs" onClick={onClose} title="关闭 (Esc)">
            <X size={16} />
          </Button>
        </header>

        <div className="flex-1 overflow-y-auto p-4">
          <div className="grid gap-4">
            {hasRunning ? (
              <Notice tone="warn">
                <span className="text-xs">当前已有日 K 任务运行中。为了避免并发打满数据源，新的手动任务会暂时禁用。</span>
              </Notice>
            ) : null}

            <div className="grid gap-4 rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3">
              <Field label="标的代码（symbol）" help="单只补拉时必填；持仓热集合和全市场增量会忽略这个输入。">
                <input
                  autoFocus
                  className="input mono"
                  onBlur={onCheckSymbol}
                  onChange={(event) => onSymbolChange(event.target.value)}
                  placeholder="如 600000、000001"
                  type="text"
                  value={symbol}
                />
              </Field>

              <div className="grid grid-cols-2 gap-3">
                <Field label="区间">
                  <select className="select" onChange={(event) => onRangeChange(event.target.value as DailyBarRange)} value={range}>
                    {RANGES.map((item) => (
                      <option key={item} value={item}>
                        {stockV2RangeLabel(item)}
                      </option>
                    ))}
                  </select>
                </Field>
                <Field label="复权">
                  <select className="select" onChange={(event) => onAdjustedChange(event.target.value as DailyBarAdjusted)} value={adjusted}>
                    {ADJUSTEDS.map((item) => (
                      <option key={item} value={item}>
                        {stockV2AdjustedLabel(item)}
                      </option>
                    ))}
                  </select>
                </Field>
              </div>

              {symbolCheck ? <SymbolQualitySummary quality={symbolCheck} symbol={symbol} /> : null}

              <div className="grid gap-2">
                <Button disabled={hasRunning || !symbol.trim()} onClick={() => onTrigger("symbol")} tone="primary">
                  <PlayCircle size={14} className="mr-1.5" />
                  单只补拉
                </Button>
                <Button disabled={hasRunning} onClick={() => onTrigger("hot")}>
                  <RewindCircle size={14} className="mr-1.5" />
                  持仓热集合（{range.toUpperCase()}）
                </Button>
                <Button disabled={hasRunning} onClick={() => onTrigger("universe_incremental")}>
                  <PlayCircle size={14} className="mr-1.5" />
                  全市场增量（最近约 10 日）
                </Button>
              </div>
            </div>

            <div className="rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3 text-xs leading-relaxed text-[var(--muted-strong)]">
              <div className="mb-2 flex flex-wrap items-center justify-between gap-2">
                <strong className="text-[var(--text)]">统一维护和手动补拉的关系</strong>
                <Pill tone={unifiedMaintenanceEnabled ? "good" : "neutral"}>
                  统一维护{unifiedMaintenanceEnabled ? "已开启" : "未开启"}
                </Pill>
              </div>
              <p className="m-0">
                统一维护是维护配置里的每日 23:00 低峰调度：每次刷新标的与最新价后，会逐只检查日 K，缺失、不足 250 根或陈旧时才补拉。
              </p>
              <p className="mt-2 mb-0">
                手动补拉是立即创建一次日 K 任务，可以选单只、持仓热集合或全市场增量。两者都遵守数据源打散和并发限制。
              </p>
            </div>
          </div>
        </div>
      </aside>
    </div>
  );
}

function SymbolQualitySummary({ quality, symbol }: { quality: StockV2DailyBarsQuality; symbol: string }) {
  return (
    <div className="rounded-lg border border-[var(--line)] bg-[var(--surface)] p-3 text-xs text-[var(--muted-strong)]">
      <strong className="text-[var(--text)]">{symbol} · 本地质量检查</strong>
      <div className="mt-1 flex flex-wrap gap-x-4 gap-y-1">
        <span>条数：{quality.rowCount}</span>
        <span>最早：{quality.earliestDate || "-"}</span>
        <span>最近：{quality.latestDate || "-"}</span>
        {quality.stale ? <span className="text-[var(--warn)]">已陈旧</span> : null}
        {quality.meets250 ? <span className="text-[var(--good)]">至少 250 根</span> : null}
        {quality.lastErrorMessage ? (
          <span className="text-[var(--danger)]">最后错误：{quality.lastErrorMessage}</span>
        ) : null}
      </div>
    </div>
  );
}

function DailyBarJobCard({
  expanded,
  fixedRunning,
  job,
  onToggle,
}: {
  expanded: boolean;
  fixedRunning: boolean;
  job: StockV2DailyBarJob;
  onToggle: () => void;
}) {
  const total = job.totalCount || 0;
  const success = job.successCount || 0;
  const failed = job.failedCount || 0;
  const processed = job.processedCount || 0;
  const pct = total > 0 ? Math.min(100, Math.round((processed / total) * 100)) : job.status === "completed" ? 100 : 0;
  const Icon = job.status === "completed" ? CheckCircle : job.status === "failed" ? XCircle : job.status === "running" ? ChartLine : CheckCircle;
  const iconTone = job.status === "completed" ? "var(--good)" : job.status === "failed" ? "var(--danger)" : job.status === "running" ? "var(--warn)" : "var(--muted)";

  return (
    <div className={`rounded-lg border bg-[var(--surface)] ${job.status === "running" ? "border-[rgba(199,85,8,0.28)]" : "border-[var(--line)]"}`}>
      <button
        className="flex w-full items-start gap-3 p-3 text-left hover:bg-[var(--surface-soft)]"
        onClick={onToggle}
        type="button"
      >
        <Icon size={18} style={{ color: iconTone, marginTop: 1 }} />
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2 text-sm">
            <strong className="font-medium">{stockV2DailyBarJobTypeLabel(job)}</strong>
            <Pill tone={stockV2DailyBarJobStatusTone(job)}>
              {stockV2DailyBarJobStatusLabel(job)}
            </Pill>
            {fixedRunning ? <Pill tone="warn">固定显示</Pill> : null}
            {job.range ? <Pill tone="neutral">{stockV2RangeLabel(job.range)}</Pill> : null}
            {job.adjusted ? <Pill tone="neutral">{stockV2AdjustedLabel(job.adjusted)}</Pill> : null}
            {job.symbol ? <Pill tone="neutral">{job.symbol}</Pill> : null}
            <span className="ml-auto text-xs text-[var(--muted)]">
              {formatCompactTime(firstMeaningfulTime(job.createdAt, job.startAt))} · 耗时 {formatDuration(job.startAt, job.endAt)}
            </span>
          </div>
          <div className="mt-2 flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-[var(--muted-strong)]">
            <span>总数 {total || "-"}</span>
            <span className="text-[var(--good)]">成功 {success}</span>
            <span className={failed > 0 ? "text-[var(--danger)]" : ""}>失败 {failed}</span>
            <span>处理 {processed}</span>
            {total ? <span>{pct}%</span> : null}
            {job.failedCount > 0 || job.errorMessage ? (
              <span className="text-[var(--accent)]">点击展开详情</span>
            ) : null}
          </div>
          {total || job.status === "running" ? (
            <div className="mt-2 h-1.5 w-full overflow-hidden rounded-full bg-[var(--surface-soft)]">
              <div
                className="h-full rounded-full transition-all"
                style={{
                  width: `${pct}%`,
                  background:
                    job.status === "completed"
                      ? "var(--good)"
                      : job.status === "failed"
                        ? "var(--danger)"
                        : "var(--accent)",
                }}
              />
            </div>
          ) : null}
        </div>
      </button>
      {expanded ? (
        <div className="border-t border-[var(--line)] bg-[var(--surface-soft)] px-4 py-3 text-xs">
          {job.errorMessage ? (
            <div className="mb-2 rounded border border-[rgba(207,31,50,0.22)] bg-[var(--danger-soft)] px-2 py-1.5 text-[var(--danger)]">
              整体错误：{job.errorMessage}
            </div>
          ) : null}
          {job.failedItems && job.failedItems.length > 0 ? (
            <div>
              <div className="mb-1 font-semibold text-[var(--danger)]">
                失败项（{job.failedItems.length} 只）
              </div>
              <ul className="grid max-h-56 gap-1 overflow-y-auto">
                {job.failedItems.map((item, idx) => (
                  <li className="flex gap-2 rounded border border-[var(--line)] bg-[var(--surface)] px-2 py-1" key={idx}>
                    <span className="font-mono text-[var(--text)]">{item.symbol || "?"}</span>
                    <span className="truncate text-[var(--muted-strong)]">{item.reason}</span>
                  </li>
                ))}
              </ul>
            </div>
          ) : null}
          {!job.errorMessage && (!job.failedItems || job.failedItems.length === 0) ? (
            <p className="text-[var(--muted)]">无失败项</p>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}

function mergeRunningIntoPage(running: StockV2DailyBarJob[], items: StockV2DailyBarJob[]) {
  const seen = new Set<string>();
  const merged: StockV2DailyBarJob[] = [];
  for (const job of [...running, ...items]) {
    if (!job.id || seen.has(job.id)) continue;
    seen.add(job.id);
    merged.push(job);
  }
  return merged;
}

function paginationWindow(page: number, totalPages: number): Array<number | "ellipsis"> {
  if (totalPages <= 7) {
    return Array.from({ length: totalPages }, (_, idx) => idx + 1);
  }
  const pages = new Set<number>([1, totalPages, page, page - 1, page + 1]);
  const sorted = [...pages].filter((item) => item >= 1 && item <= totalPages).sort((a, b) => a - b);
  const out: Array<number | "ellipsis"> = [];
  for (const item of sorted) {
    const previous = out[out.length - 1];
    if (typeof previous === "number" && item - previous > 1) {
      out.push("ellipsis");
    }
    out.push(item);
  }
  return out;
}

function formatDuration(start?: string, end?: string): string {
  if (!hasMeaningfulTime(start)) return "-";
  const s = new Date(start).getTime();
  const e = hasMeaningfulTime(end) ? new Date(end).getTime() : Date.now();
  if (isNaN(s) || isNaN(e)) return "-";
  const total = Math.max(0, Math.floor((e - s) / 1000));
  if (total < 60) return `${total}s`;
  const m = Math.floor(total / 60);
  const r = total % 60;
  if (m < 60) return `${m}m${r ? " " + r + "s" : ""}`;
  const h = Math.floor(m / 60);
  const rm = m % 60;
  return `${h}h${rm ? " " + rm + "m" : ""}`;
}

function firstMeaningfulTime(...times: Array<string | undefined>): string | undefined {
  return times.find(hasMeaningfulTime);
}
