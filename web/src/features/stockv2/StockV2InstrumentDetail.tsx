import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Sparkle, X } from "@phosphor-icons/react";
import type {
  DailyBarAdjusted,
  DailyBarRange,
  StockV2DailyBar,
  StockV2DailyBarJob,
  StockV2DailyBarsEnsureResult,
  StockV2DailyBarsQuality,
  StockV2Instrument,
  StockV2MinuteBar,
  StockV2AgentRun,
  StockV2Announcement,
  StockV2MaintainSymbolResult,
  StockV2StockProfile,
  StockV2StockProfileUpdateTask,
} from "../../app/types";
import type { AppActions } from "../../app/App";
import {
  stockV2AdjustedLabel,
  stockV2AgentRunStatusLabel,
  stockV2AgentRunTerminal,
  stockV2AgentRunStatusTone,
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
import { StrategyGenerationDrawer } from "./StockV2StrategyGenerationDrawer";
import { StockV2AgentRunDetailDrawer } from "./StockV2AgentExecutionLedger";

const RANGES: DailyBarRange[] = ["6m", "1y", "3y", "5y"];
const ADJUSTEDS: DailyBarAdjusted[] = ["none", "qfq", "hfq"];
type ChartMode = "daily" | "minute";

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
  const [chartMode, setChartMode] = useState<ChartMode>("daily");

  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [quality, setQuality] = useState<StockV2DailyBarsQuality | null>(null);
  const [bars, setBars] = useState<StockV2DailyBar[] | null>(null);
  const [minuteBars, setMinuteBars] = useState<StockV2MinuteBar[] | null>(null);
  const [minuteLoading, setMinuteLoading] = useState(false);
  const [minuteError, setMinuteError] = useState<string | null>(null);
  const [ensureResult, setEnsureResult] = useState<StockV2DailyBarsEnsureResult | null>(null);
  const [activeJob, setActiveJob] = useState<StockV2DailyBarJob | null>(null);
  const [profile, setProfile] = useState<StockV2StockProfile | null>(null);
  const [profileError, setProfileError] = useState<string | null>(null);
  const [profileBusy, setProfileBusy] = useState(false);
  const [profileRun, setProfileRun] = useState<StockV2AgentRun | null>(null);
  const [profileTask, setProfileTask] = useState<StockV2StockProfileUpdateTask | null>(null);
  const [maintenanceResult, setMaintenanceResult] = useState<StockV2MaintainSymbolResult | null>(null);
  const [profileRunDetailId, setProfileRunDetailId] = useState<string | null>(null);
  const [genOpen, setGenOpen] = useState(false);
  const profileRunPollRef = useRef<number | null>(null);

  // 区间/复权切换或切换标的时，清空旧 bars，避免展示不匹配的假 K 线。
  useEffect(() => {
    setLoading(true);
    setBars(null);
    setMinuteBars(null);
    setError(null);
    setMinuteError(null);
    setEnsureResult(null);
    setActiveJob(null);
    setProfile(null);
    setProfileError(null);
    setProfileRun(null);
    setProfileTask(null);
    setMaintenanceResult(null);
    setProfileRunDetailId(null);
    if (profileRunPollRef.current !== null) {
      window.clearTimeout(profileRunPollRef.current);
      profileRunPollRef.current = null;
    }
    void load();
    void loadMinuteBars();
    void loadProfile();
    void loadProfileTasks();
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

  const loadMinuteBars = useCallback(async () => {
    if (!inst) return;
    setMinuteLoading(true);
    setMinuteError(null);
    try {
      const res = await actions.api<{ items?: StockV2MinuteBar[] }>(
        `/api/stockv2/intraday/minute-bars?symbol=${encodeURIComponent(inst.symbol)}&days=5`,
      );
      const items = res.items ?? [];
      setMinuteBars(items);
      if (items.length === 0) {
        setMinuteError("同步完成，但本地仍无分钟行情。请检查行情刷新状态或服务日志里的分钟源错误。");
      }
    } catch (err) {
      setMinuteError(friendlyErr(err));
    } finally {
      setMinuteLoading(false);
    }
  }, [actions, inst]);

  const refreshMinuteBars = useCallback(async () => {
    if (!inst) return;
    setMinuteLoading(true);
    setMinuteError(null);
    try {
      await actions.api("/api/stockv2/quotes/refresh", {
        method: "POST",
        body: { symbols: [inst.symbol], triggerSource: "instrument_detail" },
        csrf: actions.csrf,
      });
      const res = await actions.api<{ items?: StockV2MinuteBar[] }>(
        `/api/stockv2/intraday/minute-bars?symbol=${encodeURIComponent(inst.symbol)}&days=5`,
      );
      const items = res.items ?? [];
      setMinuteBars(items);
      if (items.length === 0) {
        setMinuteError("同步完成，但本地仍无分钟行情。请检查行情刷新状态或服务日志里的分钟源错误。");
      }
    } catch (err) {
      setMinuteError(friendlyErr(err));
    } finally {
      setMinuteLoading(false);
    }
  }, [actions, inst]);

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

  const loadProfile = useCallback(async () => {
    if (!inst) return;
    try {
      const item = await actions.api<StockV2StockProfile>(`/api/stockv2/profiles/${encodeURIComponent(inst.symbol)}`);
      setProfile(item);
      setProfileError(null);
    } catch (err) {
      setProfile(null);
      setProfileError(friendlyErr(err));
    }
  }, [actions, inst]);

  const loadProfileTasks = useCallback(async () => {
    if (!inst) return;
    try {
      const res = await actions.api<{ items?: StockV2StockProfileUpdateTask[] }>(
        `/api/stockv2/profiles/${encodeURIComponent(inst.symbol)}/update-tasks?limit=6`,
      );
      const tasks = res.items ?? [];
      const task = tasks[0] ?? null;
      const agentTask = tasks.find((item) => item.agentRunId);
      setProfileTask(task);
      if (agentTask?.agentRunId) {
        try {
          const run = await actions.api<StockV2AgentRun>(`/api/stockv2/agent/runs/${encodeURIComponent(agentTask.agentRunId)}`);
          setProfileRun(run);
        } catch {
          setProfileRun(null);
        }
      } else {
        setProfileRun(null);
      }
    } catch {
      setProfileTask(null);
      setProfileRun(null);
    }
  }, [actions, inst]);

  const pollProfileRun = useCallback((runID: string) => {
    if (profileRunPollRef.current !== null) {
      window.clearTimeout(profileRunPollRef.current);
      profileRunPollRef.current = null;
    }
    const tick = async () => {
      try {
        const run = await actions.api<StockV2AgentRun>(`/api/stockv2/agent/runs/${encodeURIComponent(runID)}`);
        setProfileRun(run);
        if (stockV2AgentRunTerminal(run.status)) {
          setProfileBusy(false);
          await loadProfile();
          await loadProfileTasks();
          return;
        }
        profileRunPollRef.current = window.setTimeout(tick, 1500);
      } catch (err) {
        setProfileBusy(false);
        setProfileError(friendlyErr(err));
      }
    };
    void tick();
  }, [actions, loadProfile, loadProfileTasks]);

  const updateProfile = useCallback(async () => {
    if (!inst) return;
    setProfileBusy(true);
    try {
      const result = await actions.api<StockV2MaintainSymbolResult>(`/api/stockv2/assets/${encodeURIComponent(inst.symbol)}/maintain`, {
        method: "POST",
        body: { triggerSource: "manual", requestedBy: "web" },
        csrf: actions.csrf,
      });
      setMaintenanceResult(result);
      if (result.profile) setProfile(result.profile);
      setProfileRun(result.agentRun ?? null);
      setProfileError(null);
      await Promise.all([load(), loadProfile(), loadProfileTasks()]);
      if (result.agentRun) {
        actions.setToast("数据资产已维护，AI 画像总结已发起", "good");
        if (stockV2AgentRunTerminal(result.agentRun.status)) {
          setProfileBusy(false);
        } else {
          pollProfileRun(result.agentRun.id);
        }
        return;
      }
      actions.setToast(assetMaintenanceToast(result), result.item.status === "failed" ? "warn" : "good");
      setProfileBusy(false);
    } catch (err) {
      setProfileError(friendlyErr(err));
      setProfileBusy(false);
    }
  }, [actions, inst, load, loadProfile, loadProfileTasks, pollProfileRun]);

  const refreshProfileView = useCallback(async () => {
    setProfileBusy(true);
    try {
      await loadProfile();
      await loadProfileTasks();
    } finally {
      setProfileBusy(false);
    }
  }, [loadProfile, loadProfileTasks]);

  const stopPolling = useCallback(() => {
    if (pollRef.current !== null) {
      window.clearTimeout(pollRef.current);
      pollRef.current = null;
    }
  }, []);

  const stopProfileRunPolling = useCallback(() => {
    if (profileRunPollRef.current !== null) {
      window.clearTimeout(profileRunPollRef.current);
      profileRunPollRef.current = null;
    }
  }, []);

  useEffect(() => {
    return () => {
      stopPolling();
      stopProfileRunPolling();
    };
  }, [stopPolling, stopProfileRunPolling]);

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
            onClick={() => setGenOpen(true)}
            title="为该股票生成策略"
            className="px-2 py-1 text-xs"
          >
            <Sparkle size={14} className="mr-1" />
            生成策略
          </Button>
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
            {(["daily", "minute"] as ChartMode[]).map((mode) => (
              <button
                key={mode}
                onClick={() => setChartMode(mode)}
                className={`rounded px-2 py-1 text-xs transition ${
                  chartMode === mode
                    ? "bg-[var(--surface-strong)] text-[var(--text)]"
                    : "text-[var(--muted-strong)] hover:bg-[var(--surface-soft)]"
                }`}
                type="button"
              >
                {mode === "daily" ? "日 K" : "分钟"}
              </button>
            ))}
          </div>
          {chartMode === "daily" ? (
            <>
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
            </>
          ) : (
            <div className="flex items-center gap-2 text-xs text-[var(--muted-strong)]">
              <span>最近 5 天</span>
              <span>后台盘中分钟行情自动采集</span>
              <Button onClick={() => void refreshMinuteBars()} disabled={minuteLoading} className="px-3 py-1 text-xs">
                {minuteLoading ? "同步中…" : "同步分钟线"}
              </Button>
            </div>
          )}
          <div className="ml-auto flex flex-wrap items-center gap-2">
            {chartMode === "daily" ? (
              <Pill tone={qualityTone}>
                {stockV2DailyBarsQualityLabel(quality ?? undefined)}
              </Pill>
            ) : (
              <Pill tone={minuteBars?.length ? "good" : "neutral"}>
                {minuteBars?.length || 0} 根
              </Pill>
            )}
            {chartMode === "daily" && activeJob ? (
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
            {chartMode === "minute" ? (
              <>
                <span>范围：最近 5 天</span>
                <span>条数：{minuteBars?.length ?? 0}</span>
                {minuteBars?.[0]?.source ? <span>来源：{minuteBars[0].source}</span> : null}
                <span>保留策略：5 天滚动</span>
              </>
            ) : (
              <>
            <span>范围：{stockV2RangeLabel(range)}</span>
            <span>复权：{stockV2AdjustedLabel(adjusted)}</span>
            <span>最早：{quality?.earliestDate || "-"}</span>
            <span>最近：{quality?.latestDate || "-"}</span>
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
              </>
            )}
          </div>
        </section>

        {/* 图表主区 */}
        <section className="min-h-0 flex-1 overflow-y-auto p-4">
          {chartMode === "daily" && error ? (
            <Notice tone="danger">
              <strong className="block text-xs">加载失败</strong>
              <span className="mt-1 block break-words text-[11px] leading-relaxed opacity-90">{error}</span>
            </Notice>
          ) : null}
          {chartMode === "minute" && minuteError ? (
            <Notice tone="danger">
              <strong className="block text-xs">加载失败</strong>
              <span className="mt-1 block break-words text-[11px] leading-relaxed opacity-90">{minuteError}</span>
            </Notice>
          ) : null}
          {chartMode === "daily" ? (
            <StockV2KLineChart bars={bars} error={error} loading={loading && !bars?.length} mode="daily" />
          ) : (
            <StockV2KLineChart bars={minuteBars} error={minuteError} loading={minuteLoading && !minuteBars?.length} mode="minute" />
          )}
          <StockProfileSection
            busy={profileBusy}
            error={profileError}
            onRefresh={() => void refreshProfileView()}
            onUpdate={() => void updateProfile()}
            onOpenAgentRun={(runId) => setProfileRunDetailId(runId)}
            profile={profile}
            agentRun={profileRun}
            maintenanceResult={maintenanceResult}
            updateTask={profileTask}
          />
        </section>

        <footer className="border-t border-[var(--line)] px-4 py-2 text-[11px] text-[var(--muted)]">
          数据来源：{chartMode === "minute" ? minuteBars?.[0]?.source || "分钟行情待同步" : bars?.[0]?.source || quality?.source || "tencent_fqkline"}（公开端点，异步落盘）。失败时不会伪造 K 线。
        </footer>
      </aside>
      {genOpen && inst ? (
        <StrategyGenerationDrawer
          actions={actions}
          initial={{
            mode: "single_instrument",
            targetInstruments: [{ symbol: inst.symbol, market: inst.market, name: inst.name }],
          }}
          onClose={() => setGenOpen(false)}
        />
      ) : null}
      {profileRunDetailId ? (
        <StockV2AgentRunDetailDrawer
          actions={actions}
          runId={profileRunDetailId}
          onClose={() => setProfileRunDetailId(null)}
        />
      ) : null}
    </div>
  );
}

function StockProfileSection({
  agentRun,
  busy,
  error,
  onOpenAgentRun,
  onRefresh,
  onUpdate,
  profile,
  maintenanceResult,
  updateTask,
}: {
  agentRun: StockV2AgentRun | null;
  busy: boolean;
  error: string | null;
  onOpenAgentRun: (runId: string) => void;
  onRefresh: () => void;
  onUpdate: () => void;
  profile: StockV2StockProfile | null;
  maintenanceResult: StockV2MaintainSymbolResult | null;
  updateTask: StockV2StockProfileUpdateTask | null;
}) {
  const aiTone = profile?.aiProfileStatus === "ready" ? "good" : profile?.aiProfileStatus === "failed" ? "danger" : profile?.aiProfileStatus === "not_configured" ? "warn" : "neutral";
  const runTone = agentRun ? stockV2AgentRunStatusTone(agentRun.status) as Tone : "neutral";
  const taskTone =
    updateTask?.status === "failed" ? "danger" :
    updateTask?.status === "partial" || updateTask?.status === "running" ? "warn" :
    updateTask?.status === "completed" ? "good" :
    "neutral";
  return (
    <section className="mt-4 rounded-lg border border-[var(--line)] bg-[var(--surface-soft)]">
      <div className="flex flex-wrap items-start justify-between gap-3 border-b border-[var(--line)] px-3 py-3">
        <div>
          <h4 className="m-0 text-sm font-semibold">数据资产维护</h4>
          <p className="muted mt-1 mb-0 text-xs">补齐日 K 缺口，检查基础画像和公告/重大事项，并按条件触发 AI 画像总结。</p>
        </div>
        <div className="flex flex-wrap gap-2">
          <Button className="px-2 py-1 text-xs" onClick={onRefresh} disabled={busy}>重新读取</Button>
          <Button className="px-2 py-1 text-xs" onClick={onUpdate} disabled={busy} tone="primary">{busy ? "维护中…" : "维护该标的数据资产"}</Button>
        </div>
      </div>
      <div className="grid gap-3 p-3 text-sm">
        {maintenanceResult?.item ? (
          <AssetMaintenanceResultSummary
            announcements={maintenanceResult.announcements || []}
            item={maintenanceResult.item}
            onOpenAgentRun={onOpenAgentRun}
          />
        ) : null}
        {agentRun ? (
          <Notice tone={agentRun.status === "failed" ? "danger" : "warn"}>
            <span className="text-xs">
              AI 任务 {stockV2AgentRunStatusLabel(agentRun.status)}
              <Pill tone={runTone} className="ml-2">{agentRun.id}</Pill>
              {agentRun.errorMessage ? <span className="ml-2 break-words">{agentRun.errorMessage}</span> : null}
              <Button className="ml-2 px-2 py-0.5 text-xs" onClick={() => onOpenAgentRun(agentRun.id)}>
                查看执行详情
              </Button>
            </span>
          </Notice>
        ) : null}
        {error && !profile ? <Notice tone="warn"><span className="text-xs">画像未就绪：{error}</span></Notice> : null}
        {profile ? (
          <>
            <div className="flex flex-wrap gap-2">
              <Pill tone="good">基础已生成</Pill>
              <Pill tone={aiTone}>AI {profile.aiProfileStatus || "missing"}</Pill>
              {profile.aiProfileModel ? <Pill tone="neutral">{profile.aiProfileModel}</Pill> : null}
              {profile.aiProfileConfidence ? <Pill tone="neutral">置信 {Math.round(profile.aiProfileConfidence * 100)}%</Pill> : null}
            </div>
            {updateTask ? (
              <div className="flex flex-wrap items-center gap-1.5 text-xs text-[var(--muted)]">
                <Pill tone={taskTone}>{stockProfileUpdateStatusLabel(updateTask.status)}</Pill>
                <Pill tone="neutral">{stockProfileTriggerLabel(updateTask.triggerSource)}</Pill>
                <Pill tone={updateTask.baseInputChanged ? "warn" : "neutral"}>
                  {updateTask.baseInputChanged ? "输入已变化" : "输入无变化"}
                </Pill>
                {updateTask.baseProfileStatus ? (
                  <Pill tone={stockProfileResultTone(updateTask.baseProfileStatus)}>
                    {stockProfileBaseStatusLabel(updateTask.baseProfileStatus)}
                  </Pill>
                ) : null}
                <Pill tone={stockProfileAIDecisionTone(updateTask.aiDecision)}>
                  {stockProfileAIDecisionLabel(updateTask.aiDecision)}
                </Pill>
                {updateTask.aiProfileStatus ? (
                  <Pill tone={stockProfileResultTone(updateTask.aiProfileStatus)}>
                    {stockProfileAIStatusLabel(updateTask.aiProfileStatus)}
                  </Pill>
                ) : null}
                {updateTask.agentRunId ? (
                  <Button className="px-2 py-0.5 text-xs" onClick={() => onOpenAgentRun(updateTask.agentRunId!)}>
                    Agent {updateTask.agentRunId.slice(0, 12)}
                  </Button>
                ) : null}
                {updateTask.aiProfileError ? <span className="break-words text-[var(--danger)]">{updateTask.aiProfileError}</span> : null}
                <span>{formatTime(updateTask.finishedAt || updateTask.updatedAt)}</span>
              </div>
            ) : null}
            {updateTask?.sourceStatuses?.length ? (
              <ProfileTerms
                label="资料源"
                values={updateTask.sourceStatuses.map((item) => `${item.source}:${item.status}`)}
              />
            ) : null}
            <ProfileRow label="摘要" value={profile.businessSummaryZh || profile.businessSummary || profile.businessSummaryEn} />
            <ProfileRow label="行业" value={profile.industry} />
            <ProfileTerms label="关键词" values={[...(profile.keywordsZh || []), ...(profile.keywordsEn || [])]} />
            <ProfileTerms label="业务线" values={[...(profile.businessLinesZh || []), ...(profile.businessLinesEn || [])]} />
            <ProfileTerms label="风险标签" values={[...(profile.riskTagsZh || []), ...(profile.riskTagsEn || [])]} />
            {profile.aiProfileError ? <ProfileRow label="AI 错误" value={profile.aiProfileError} danger /> : null}
            <BilingualProfileDetails profile={profile} />
            <details className="rounded border border-[var(--line)] bg-[var(--surface)] p-3">
              <summary className="cursor-pointer text-xs font-medium">完整 profile text（中英文）</summary>
              <ProfileTextBlock title="中文" value={profile.profileTextZh || profile.profileText} />
              <ProfileTextBlock title="English" value={profile.profileTextEn} />
            </details>
          </>
        ) : !error ? (
          <p className="m-0 text-xs text-[var(--muted)]">画像加载中...</p>
        ) : null}
      </div>
    </section>
  );
}

function BilingualProfileDetails({ profile }: { profile: StockV2StockProfile }) {
  return (
    <details className="rounded border border-[var(--line)] bg-[var(--surface)] p-3">
      <summary className="cursor-pointer text-xs font-medium">AI 总结画像（中英文）</summary>
      <div className="mt-3 grid gap-3">
        <ProfileRow label="中文摘要" value={profile.businessSummaryZh || profile.businessSummary} />
        <ProfileTerms label="中文关键词" values={profile.keywordsZh || []} />
        <ProfileTerms label="中文业务线" values={profile.businessLinesZh || []} />
        <ProfileTerms label="中文风险" values={profile.riskTagsZh || []} />
        <ProfileRow label="EN Summary" value={profile.businessSummaryEn} />
        <ProfileTerms label="EN Keywords" values={profile.keywordsEn || []} />
        <ProfileTerms label="EN Lines" values={profile.businessLinesEn || []} />
        <ProfileTerms label="EN Risks" values={profile.riskTagsEn || []} />
      </div>
    </details>
  );
}

function AssetMaintenanceResultSummary({
  announcements,
  item,
  onOpenAgentRun,
}: {
  announcements: StockV2Announcement[];
  item: StockV2MaintainSymbolResult["item"];
  onOpenAgentRun: (runId: string) => void;
}) {
  const major = announcements.filter((ann) => ann.major);
  return (
    <div className="rounded border border-[var(--line)] bg-[var(--surface)] p-3">
      <div className="mb-2 flex flex-wrap items-center gap-1.5 text-xs">
        <Pill tone={item.status === "failed" ? "danger" : item.status === "partial" ? "warn" : "good"}>
          {assetMaintenanceStatusLabel(item.status)}
        </Pill>
        <Pill tone="neutral">日 K {item.dailyBarStatus || "-"} {item.dailyBarFetched ? `+${item.dailyBarFetched}` : ""}</Pill>
        <Pill tone={item.baseProfileChanged ? "warn" : "neutral"}>基础画像 {item.baseProfileStatus || "-"}</Pill>
        <Pill tone={item.majorAnnouncementsNew > 0 ? "warn" : "neutral"}>公告 {item.announcementsNew} / 重大 {item.majorAnnouncementsNew}</Pill>
        <Pill tone={item.aiDecision?.startsWith("called") ? "warn" : "neutral"}>AI {item.aiDecision || "-"}</Pill>
        {item.agentRunId ? (
          <Button className="px-2 py-0.5 text-xs" onClick={() => onOpenAgentRun(item.agentRunId!)}>
            Agent {item.agentRunId.slice(0, 12)}
          </Button>
        ) : null}
        <span className="text-[var(--muted)]">{formatDurationMs(item.durationMs)}</span>
      </div>
      {item.errorMessage ? <div className="mb-2 text-xs text-[var(--danger)]">{item.errorMessage}</div> : null}
      {announcements.length ? (
        <div className="grid gap-1 text-xs text-[var(--muted-strong)]">
          {(major.length ? major : announcements).slice(0, 4).map((ann) => (
            <div className="flex min-w-0 items-center gap-2" key={ann.id}>
              <Pill tone={ann.major ? "warn" : "neutral"}>{ann.major ? ann.majorReason || "重大" : "公告"}</Pill>
              <span className="truncate">{ann.title}</span>
            </div>
          ))}
        </div>
      ) : null}
    </div>
  );
}

function ProfileTextBlock({ title, value }: { title: string; value?: string }) {
  return (
    <div className="mt-3">
      <div className="mb-1 text-[11px] font-medium text-[var(--muted)]">{title}</div>
      <pre className="max-h-56 overflow-auto whitespace-pre-wrap rounded border border-[var(--line)] bg-[var(--surface-soft)] p-2 text-xs text-[var(--muted-strong)]">
        {value || "暂无"}
      </pre>
    </div>
  );
}

function ProfileRow({ danger, label, value }: { danger?: boolean; label: string; value?: string }) {
  if (!value) return null;
  return (
    <div className="grid grid-cols-[88px_minmax(0,1fr)] gap-2 text-xs">
      <span className="text-[var(--muted)]">{label}</span>
      <span className={danger ? "break-words text-[var(--danger)]" : "break-words text-[var(--muted-strong)]"}>{value}</span>
    </div>
  );
}

function ProfileTerms({ label, values }: { label: string; values?: string[] }) {
  const clean = Array.from(new Set((values || []).filter(Boolean))).slice(0, 24);
  if (clean.length === 0) return null;
  return (
    <div className="grid grid-cols-[88px_minmax(0,1fr)] gap-2 text-xs">
      <span className="text-[var(--muted)]">{label}</span>
      <div className="flex flex-wrap gap-1">
        {clean.map((item) => <Pill key={item} tone="neutral">{item}</Pill>)}
      </div>
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

function assetMaintenanceToast(result: StockV2MaintainSymbolResult): string {
  if (result.item.agentRunId) return "数据资产已维护，AI 画像总结已发起";
  if (result.item.aiDecision === "skipped_budget_exhausted") return "数据资产已维护，本轮 AI 预算已耗尽";
  if (result.item.aiDecision === "skipped_not_configured") return "数据资产已维护，AI 未配置";
  return "数据资产已维护";
}

function assetMaintenanceStatusLabel(status?: string): string {
  switch (status) {
    case "completed":
      return "已完成";
    case "partial":
      return "部分完成";
    case "failed":
      return "失败";
    case "running":
      return "运行中";
    default:
      return status || "-";
  }
}

function formatDurationMs(ms?: number): string {
  if (!ms || ms <= 0) return "-";
  if (ms < 1000) return `${ms} ms`;
  return `${(ms / 1000).toFixed(ms < 10000 ? 1 : 0)} 秒`;
}

function stockProfileUpdateStatusLabel(status?: string): string {
  if (status === "running") return "运行中";
  if (status === "completed") return "已完成";
  if (status === "partial") return "部分完成";
  if (status === "failed") return "失败";
  return status || "未知";
}

function stockProfileTriggerLabel(trigger?: string): string {
  if (trigger === "auto") return "自动更新";
  if (trigger === "manual") return "手动更新";
  return trigger || "未知触发";
}

function stockProfileAIDecisionLabel(decision?: string): string {
  if (decision === "called") return "AI 已发起";
  if (decision === "skipped_unchanged") return "AI 跳过：输入无变化";
  if (decision === "skipped_not_configured") return "AI 跳过：未配置";
  if (decision === "skipped_unavailable") return "AI 跳过：不可用";
  if (decision === "failed") return "AI 失败";
  return decision || "AI 未运行";
}

function stockProfileAIDecisionTone(decision?: string): Tone {
  if (decision === "called") return "warn";
  if (decision === "failed") return "danger";
  if (decision === "skipped_unavailable") return "warn";
  return "neutral";
}

function stockProfileBaseStatusLabel(status?: string): string {
  if (status === "ready") return "基础已生成";
  if (status === "failed") return "基础失败";
  return status || "基础未知";
}

function stockProfileAIStatusLabel(status?: string): string {
  if (status === "ready") return "AI 生成成功";
  if (status === "running") return "AI 生成中";
  if (status === "failed") return "AI 生成失败";
  if (status === "not_configured") return "AI 未配置";
  if (status === "missing") return "AI 未生成";
  return status || "AI 未知";
}

function stockProfileResultTone(status?: string): Tone {
  if (status === "ready") return "good";
  if (status === "running" || status === "not_configured" || status === "missing") return "warn";
  if (status === "failed") return "danger";
  return "neutral";
}

function formatTime(iso?: string): string {
  if (!iso) return "-";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleString("zh-CN", { hour12: false });
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
