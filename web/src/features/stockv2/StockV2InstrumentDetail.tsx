import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { X } from "@phosphor-icons/react";
import type {
  DailyBarAdjusted,
  DailyBarRange,
  StockV2DailyBar,
  StockV2DailyBarJob,
  StockV2DailyBarsEnsureResult,
  StockV2DailyBarsQuality,
  StockV2Instrument,
} from "../../app/types";
import type { AppActions } from "../../app/App";
import {
  stockV2AdjustedLabel,
  stockV2DailyBarJobStatusLabel,
  stockV2DailyBarJobStatusTone,
  stockV2DailyBarJobTypeLabel,
  stockV2DailyBarsQualityLabel,
  stockV2DailyBarsQualityTone,
  stockV2InstrumentTypeLabel,
  stockV2RangeLabel,
} from "../../domain/labels";
import type { Tone } from "../../app/types";
import { Button, Pill, Notice } from "../../components/ui";
import { StockV2KLineChart } from "./StockV2KLineChart";

const RANGES: DailyBarRange[] = ["6m", "1y", "3y", "5y"];
const ADJUSTEDS: DailyBarAdjusted[] = ["none", "qfq", "hfq"];

/**
 * StockV2InstrumentDetail：右侧 680px Drawer 查看单只标的详情（K 线 + 质量 + Job）。
 *
 * 数据流：
 *   open(onClose,inst) → GET quality
 *     → 质量 ok 且本地有覆盖 → GET daily 立即出图
 *     → 质量 stale / empty / 未覆盖 → POST ensure(异步 job) → 每 2s GET jobs 直到该 jobId
 *       不是 running → 再 GET quality + GET daily → 出图。
 * 错误时显示 Notice，绝不画假 K 线。
 */
export function StockV2InstrumentDetail({
  inst,
  onClose,
  actions,
}: {
  inst: StockV2Instrument | null | undefined;
  onClose: () => void;
  actions: AppActions;
}) {
  const [range, setRange] = useState<DailyBarRange>("1y");
  const [adjusted, setAdjusted] = useState<DailyBarAdjusted>("none");

  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [quality, setQuality] = useState<StockV2DailyBarsQuality | null>(null);
  const [bars, setBars] = useState<StockV2DailyBar[] | null>(null);
  const [ensureResult, setEnsureResult] = useState<StockV2DailyBarsEnsureResult | null>(null);
  const [activeJob, setActiveJob] = useState<StockV2DailyBarJob | null>(null);

  // 区间/复权切换或切换标的时，清空旧 bars，避免展示不匹配的假 K 线。
  useEffect(() => {
    setBars(null);
    setError(null);
    setEnsureResult(null);
    setActiveJob(null);
    void load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [inst?.symbol, range, adjusted]);

  // ESC 关闭
  useEffect(() => {
    if (!inst) return;
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") onClose();
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [inst, onClose]);

  const queryRange = useMemo(() => getRangeBound(range), [range]);
  const pollRef = useRef<number | null>(null);

  const fetchQuality = useCallback(async (): Promise<StockV2DailyBarsQuality> => {
    return actions.api<StockV2DailyBarsQuality>(
      `/api/stockv2/history/daily/quality?symbol=${encodeURIComponent(inst!.symbol)}&adjusted=${adjusted}`,
    );
  }, [actions, adjusted, inst]);

  const fetchBars = useCallback(async (): Promise<StockV2DailyBar[]> => {
    const res = await actions.api<{ items?: StockV2DailyBar[] }>(
      `/api/stockv2/history/daily?symbol=${encodeURIComponent(inst!.symbol)}&adjusted=${adjusted}&start=${queryRange.start}&end=${queryRange.end}`,
    );
    return res.items ?? [];
  }, [actions, adjusted, inst, queryRange]);

  const load = useCallback(async () => {
    if (!inst) return;
    setLoading(true);
    setError(null);
    try {
      const q = await fetchQuality();
      setQuality(q);

      // 判断是否需要补拉
      const needsEnsure =
        !q.hasData ||
        q.stale ||
        (q.earliestDate && q.earliestDate > queryRange.start);

      if (!needsEnsure) {
        const bs = await fetchBars();
        setBars(bs);
      } else {
        // 启动异步 ensure
        const r = await actions.api<StockV2DailyBarsEnsureResult>("/api/stockv2/history/daily/ensure", {
          method: "POST",
          body: { symbol: inst.symbol, range, adjusted },
          csrf: actions.csrf,
        });
        setEnsureResult(r);
        if (r.skipped) {
          const bs = await fetchBars();
          setBars(bs);
        } else if (r.jobId) {
          setActiveJob({
            id: r.jobId,
            jobType: "daily_bars_ensure",
            mode: "symbol",
            symbol: inst.symbol,
            status: "running",
            totalCount: 1,
            processedCount: 0,
            successCount: 0,
            failedCount: 0,
            startAt: new Date().toISOString(),
            endAt: "",
            createdAt: new Date().toISOString(),
          });
          startPolling(r.jobId);
        } else {
          // 无 job 也没 skip 的异常情况：尝试直接拉
          const bs = await fetchBars();
          setBars(bs);
        }
      }
    } catch (err) {
      setError(friendlyErr(err));
    } finally {
      setLoading(false);
    }
  }, [inst, fetchQuality, fetchBars, actions, range, adjusted, queryRange]);

  const startPolling = useCallback((jobId: string) => {
    stopPolling();
    const tick = async () => {
      try {
        const j = await actions.api<StockV2DailyBarJob>(
          `/api/stockv2/history/daily/jobs/${encodeURIComponent(jobId)}`,
        );
        setActiveJob(j);
        if (j.status !== "running") {
          stopPolling();
          setLoading(true);
          try {
            const q = await fetchQuality();
            setQuality(q);
            const bs = await fetchBars();
            setBars(bs);
            if (j.status === "failed") {
              setError(j.errorMessage || "日 K 补拉失败，请稍后重试");
            }
          } catch (err) {
            setError(friendlyErr(err));
          } finally {
            setLoading(false);
          }
          return;
        }
      } catch {
        /* 轮询失败不打断，继续下一轮 */
      }
      pollRef.current = window.setTimeout(tick, 2000);
    };
    pollRef.current = window.setTimeout(tick, 1500);
  }, [actions, fetchQuality, fetchBars]);

  const stopPolling = useCallback(() => {
    if (pollRef.current !== null) {
      window.clearTimeout(pollRef.current);
      pollRef.current = null;
    }
  }, []);

  useEffect(() => {
    return () => stopPolling();
  }, [stopPolling]);

  if (!inst) return null;

  const latest = bars && bars.length ? bars[bars.length - 1] : null;
  const qualityTone = stockV2DailyBarsQualityTone(quality ?? undefined) as Tone;
  const jobTone: Tone | undefined = activeJob ? stockV2DailyBarJobStatusTone(activeJob) as Tone : undefined;

  return (
    <div
      className="fixed inset-0 z-50"
      role="presentation"
      onClick={onClose}
    >
      <div className="absolute inset-0 bg-[rgba(16,18,22,0.56)]" />
      <aside
        className="absolute right-0 top-0 flex h-full w-[min(680px,100vw)] flex-col border-l border-[var(--line)] bg-[var(--surface)] shadow-[var(--shadow)]"
        onClick={(e) => e.stopPropagation()}
        role="dialog"
        aria-modal="true"
        aria-label={`${inst.name || inst.symbol} 详情`}
      >
        {/* 头部 */}
        <header className="flex items-start gap-3 border-b border-[var(--line)] p-4">
          <div className="min-w-0 flex-1">
            <div className="flex flex-wrap items-baseline gap-2">
              <h3 className="m-0 text-base font-semibold text-[var(--text)]">{inst.name || inst.symbol}</h3>
              <Pill tone="neutral">
                <span className="font-mono">{inst.symbol}</span>
              </Pill>
              <Pill tone="neutral">{inst.market || "-"}</Pill>
              <Pill tone="neutral">{stockV2InstrumentTypeLabel(inst.instrumentType)}</Pill>
              {inst.status && inst.status !== "active" ? (
                <Pill tone="warn">{inst.status}</Pill>
              ) : null}
            </div>
            <div className="mt-1.5 flex flex-wrap items-baseline gap-3 text-xs text-[var(--muted-strong)]">
              {inst.sector ? <span>行业：{inst.sector}</span> : null}
              {inst.listDate ? <span>上市：{inst.listDate}</span> : null}
              {latest ? (
                <span>
                  <span className="mr-1">最近收盘</span>
                  <span className={`font-mono font-semibold ${latest.close >= latest.open ? "text-[var(--danger)]" : "text-[var(--good)]"}`}>
                    ¥{latest.close.toFixed(2)}
                  </span>
                  <span className="ml-2 text-[var(--muted)]">{latest.tradeDate}</span>
                </span>
              ) : null}
            </div>
          </div>
          <Button
            onClick={onClose}
            aria-label="关闭"
            title="关闭 (Esc)"
            className="px-2 py-1 text-xs"
          >
            <X size={16} />
          </Button>
        </header>

        {/* 控制条 */}
        <section className="flex flex-wrap items-center gap-3 border-b border-[var(--line)] px-4 py-3">
          <div className="flex gap-1 rounded-md border border-[var(--line)] p-0.5">
            {RANGES.map((r) => (
              <button
                key={r}
                onClick={() => setRange(r)}
                className={`rounded px-2 py-1 text-xs transition ${
                  range === r
                    ? "bg-[var(--surface-strong)] text-[var(--text)]"
                    : "text-[var(--muted-strong)] hover:bg-[var(--surface-soft)]"
                }`}
                type="button"
              >
                {r.toUpperCase()}
              </button>
            ))}
          </div>
          <div className="flex gap-1 rounded-md border border-[var(--line)] p-0.5">
            {ADJUSTEDS.map((a) => (
              <button
                key={a}
                onClick={() => setAdjusted(a)}
                className={`rounded px-2 py-1 text-xs transition ${
                  adjusted === a
                    ? "bg-[var(--surface-strong)] text-[var(--text)]"
                    : "text-[var(--muted-strong)] hover:bg-[var(--surface-soft)]"
                }`}
                type="button"
              >
                {stockV2AdjustedLabel(a)}
              </button>
            ))}
          </div>
          <Button
            onClick={load}
            disabled={loading}
            className={`px-3 py-1 text-xs ${loading ? "opacity-60" : ""}`}
          >
            {loading ? "加载中…" : "刷新日 K"}
          </Button>
          <div className="ml-auto flex flex-wrap items-center gap-2">
            <Pill tone={qualityTone}>
              {stockV2DailyBarsQualityLabel(quality ?? undefined)}
            </Pill>
            {activeJob ? (
              <Pill tone={jobTone ?? "warn"}>
                {stockV2DailyBarJobTypeLabel(activeJob)} · {stockV2DailyBarJobStatusLabel(activeJob)}
                {activeJob.totalCount > 0
                  ? ` (${activeJob.processedCount}/${activeJob.totalCount})`
                  : ""}
              </Pill>
            ) : null}
          </div>
        </section>

        {/* 质量细项 */}
        <section className="border-b border-[var(--line)] px-4 py-2 text-[11px] text-[var(--muted-strong)]">
          <div className="flex flex-wrap gap-x-4 gap-y-1">
            <span>范围：{stockV2RangeLabel(range)}</span>
            <span>复权：{stockV2AdjustedLabel(adjusted)}</span>
            <span>最早：{quality?.earliestDate || "—"}</span>
            <span>最近：{quality?.latestDate || "—"}</span>
            <span>条数：{quality?.rowCount ?? 0}</span>
            {quality?.source ? <span>来源：{quality.source}</span> : null}
            {quality?.stale ? (
              <span className="text-[var(--warn)]">已陈旧（超过 5 自然日）</span>
            ) : null}
            {quality?.meets250 ? (
              <span className="text-[var(--good)]">满足 ≥250 根</span>
            ) : quality?.hasData ? (
              <span className="text-[var(--muted)]">未达 250 根</span>
            ) : null}
            {quality?.lastErrorMessage ? (
              <span className="text-[var(--danger)]">最后错误：{quality.lastErrorMessage}</span>
            ) : null}
            {ensureResult?.skipped ? (
              <span className="text-[var(--muted)]">本地命中，跳过抓取</span>
            ) : null}
          </div>
        </section>

        {/* 图表主区 */}
        <section className="min-h-0 flex-1 overflow-y-auto p-4">
          {error ? (
            <Notice tone="danger">
              <strong className="block text-xs">加载失败</strong>
              <span className="mt-1 block break-words text-[11px] leading-relaxed opacity-90">{error}</span>
            </Notice>
          ) : null}
          <StockV2KLineChart bars={bars} error={error} loading={loading && !bars?.length} />
        </section>

        <footer className="border-t border-[var(--line)] px-4 py-2 text-[11px] text-[var(--muted)]">
          数据来源：{bars?.[0]?.source || quality?.source || "tencent_fqkline"}（公开端点，异步落盘）。失败时不会伪造 K 线。
        </footer>
      </aside>
    </div>
  );
}

function getRangeBound(r: DailyBarRange): { start: string; end: string } {
  const end = new Date();
  const start = new Date();
  const days = r === "6m" ? 183 : r === "1y" ? 365 : r === "3y" ? 1096 : 1826;
  start.setDate(end.getDate() - days);
  return {
    start: start.toISOString().slice(0, 10),
    end: end.toISOString().slice(0, 10),
  };
}

function friendlyErr(err: unknown): string {
  if (!err) return "未知错误";
  if (typeof err === "string") return err;
  if (err instanceof Error) return err.message;
  try {
    return JSON.stringify(err);
  } catch {
    return String(err);
  }
}
