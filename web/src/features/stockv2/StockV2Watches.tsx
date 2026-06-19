import { Archive, ArrowsClockwise, Check, CheckCircle, MagnifyingGlass, Pause, Play, Plus, X } from "@phosphor-icons/react";
import { useEffect, useMemo, useRef, useState } from "react";
import type { AppActions } from "../../app/App";
import type {
  AppData,
  StockV2Alert,
  StockV2AlertListResponse,
  StockV2Instrument,
  StockV2Portfolio,
  StockV2Watch,
  StockV2WatchInput,
  StockV2WatchListResponse,
  StockV2WatchRunResult,
  StockV2WatchScheduleKind,
  StockV2WatchTriggerKind,
} from "../../app/types";
import { friendlyError } from "../../api/client";
import { Button, Drawer, Field, Notice, Panel, Pill, useDangerConfirm } from "../../components/ui";
import {
  formatDate,
  stockV2AlertLevelLabel,
  stockV2AlertLevelTone,
  stockV2AlertStatusLabel,
  stockV2AlertStatusTone,
  stockV2WatchRuleSummary,
  stockV2WatchRunStatusTone,
  stockV2WatchScheduleLabel,
  stockV2WatchSourceLabel,
  stockV2WatchStatusLabel,
  stockV2WatchStatusTone,
  stockV2WatchTriggerIsPercent,
  stockV2WatchTriggerLabel,
} from "../../domain/labels";

// Watch = 长期盯盘对象;Trigger = Watch 内确定性规则;Alert = 规则命中提醒台账。
// 本轮只跑通「盯住对象 → 规则命中 → 可追踪提醒 → 确认/忽略/解决」,
// 不接 Agent、不接 Review、不生成买卖建议、不改持仓。
// 后端 watch/alert 接口尚未合并:404/异常时页面降级为轻量错误,不崩溃。

const WATCH_PAGE_SIZE = 10;
const ALERT_PAGE_SIZE = 10;
const DEFAULT_COOLDOWN = 300;

interface SymbolRef {
  symbol: string;
  market?: string;
  name?: string;
}

export function StockV2Watches({ actions, data }: { actions: AppActions; data: AppData }) {
  const portfolios = data.stockv2.portfolios || [];

  const [watches, setWatches] = useState<StockV2Watch[]>([]);
  const [watchTotal, setWatchTotal] = useState(0);
  const [watchPage, setWatchPage] = useState(1);
  const [watchLoading, setWatchLoading] = useState(true);
  const [watchError, setWatchError] = useState<string | null>(null);

  const [watchStatus, setWatchStatus] = useState("all");
  const [watchPortfolio, setWatchPortfolio] = useState("all");
  const [watchKeyword, setWatchKeyword] = useState("");

  const [alerts, setAlerts] = useState<StockV2Alert[]>([]);
  const [alertTotal, setAlertTotal] = useState(0);
  const [alertPage, setAlertPage] = useState(1);
  const [alertLoading, setAlertLoading] = useState(true);
  const [alertError, setAlertError] = useState<string | null>(null);
  const [alertStatus, setAlertStatus] = useState("all");

  const [createOpen, setCreateOpen] = useState(false);
  const [runResult, setRunResult] = useState<StockV2WatchRunResult | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const { confirmDanger, dangerConfirmDialog } = useDangerConfirm();

  async function fetchWatches(nextPage = watchPage) {
    setWatchLoading(true);
    setWatchError(null);
    try {
      const safePage = Math.max(1, nextPage);
      const params = new URLSearchParams({
        limit: String(WATCH_PAGE_SIZE),
        offset: String((safePage - 1) * WATCH_PAGE_SIZE),
      });
      if (watchStatus !== "all") params.set("status", watchStatus);
      if (watchPortfolio !== "all") params.set("portfolioId", watchPortfolio);
      if (watchKeyword.trim()) params.set("symbol", watchKeyword.trim());

      const res = await actions.api<StockV2WatchListResponse>(`/api/stockv2/watches?${params.toString()}`);
      const total = res.total ?? res.items?.length ?? 0;
      const limit = res.limit || WATCH_PAGE_SIZE;
      const resolvedOffset = res.offset ?? (safePage - 1) * WATCH_PAGE_SIZE;
      if (total > 0 && resolvedOffset >= total) {
        setWatchPage(Math.max(1, Math.ceil(total / limit)));
        return;
      }
      setWatches(res.items || []);
      setWatchTotal(total);
    } catch (err) {
      setWatchError(friendlyError(err));
      setWatches([]);
      setWatchTotal(0);
    } finally {
      setWatchLoading(false);
    }
  }

  async function fetchAlerts(nextPage = alertPage) {
    setAlertLoading(true);
    setAlertError(null);
    try {
      const safePage = Math.max(1, nextPage);
      const params = new URLSearchParams({
        limit: String(ALERT_PAGE_SIZE),
        offset: String((safePage - 1) * ALERT_PAGE_SIZE),
      });
      if (alertStatus !== "all") params.set("status", alertStatus);

      const res = await actions.api<StockV2AlertListResponse>(`/api/stockv2/alerts?${params.toString()}`);
      const total = res.total ?? res.items?.length ?? 0;
      const limit = res.limit || ALERT_PAGE_SIZE;
      const resolvedOffset = res.offset ?? (safePage - 1) * ALERT_PAGE_SIZE;
      if (total > 0 && resolvedOffset >= total) {
        setAlertPage(Math.max(1, Math.ceil(total / limit)));
        return;
      }
      setAlerts(res.items || []);
      setAlertTotal(total);
    } catch (err) {
      setAlertError(friendlyError(err));
      setAlerts([]);
      setAlertTotal(0);
    } finally {
      setAlertLoading(false);
    }
  }

  useEffect(() => {
    void fetchWatches(watchPage);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [watchPage, watchStatus, watchPortfolio, watchKeyword]);

  useEffect(() => {
    void fetchAlerts(alertPage);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [alertPage, alertStatus]);

  const watchTotalPages = Math.max(1, Math.ceil(watchTotal / WATCH_PAGE_SIZE));
  const watchPageNumbers = useMemo(() => paginationWindow(watchPage, watchTotalPages), [watchPage, watchTotalPages]);
  const alertTotalPages = Math.max(1, Math.ceil(alertTotal / ALERT_PAGE_SIZE));
  const alertPageNumbers = useMemo(() => paginationWindow(alertPage, alertTotalPages), [alertPage, alertTotalPages]);

  async function refreshAfter(label: string, fn: () => Promise<unknown>) {
    setSubmitting(true);
    try {
      await fn();
      actions.setToast(label, "good");
      await Promise.all([fetchWatches(), fetchAlerts()]);
    } catch (err) {
      actions.setToast(friendlyError(err), "danger");
    } finally {
      setSubmitting(false);
    }
  }

  async function runWatch(watch: StockV2Watch) {
    setSubmitting(true);
    try {
      const res = await actions.api<StockV2WatchRunResult>(`/api/stockv2/watches/${watch.id}/run`, { method: "POST" });
      setRunResult(res);
      actions.setToast("已执行检查", "good");
      await Promise.all([fetchWatches(), fetchAlerts()]);
    } catch (err) {
      actions.setToast(friendlyError(err), "danger");
    } finally {
      setSubmitting(false);
    }
  }

  function changeWatchStatus(watch: StockV2Watch, action: "activate" | "pause" | "archive") {
    return actions.api(`/api/stockv2/watches/${watch.id}/${action}`, { method: "POST" });
  }

  async function requestPause(watch: StockV2Watch) {
    await refreshAfter("已暂停盯盘", () => changeWatchStatus(watch, "pause"));
  }

  async function requestArchive(watch: StockV2Watch) {
    const ok = await confirmDanger({
      title: "归档盯盘",
      body: "归档后该盯盘不再执行检查。归档不删除已有 Alert 台账,仍可回看。",
      objectName: watch.name || watch.id,
      confirmLabel: "归档",
    });
    if (ok) await refreshAfter("已归档盯盘", () => changeWatchStatus(watch, "archive"));
  }

  async function changeAlertStatus(alert: StockV2Alert, action: "ack" | "ignore" | "resolve") {
    await actions.api(`/api/stockv2/alerts/${alert.id}/${action}`, { method: "POST" });
  }

  async function requestIgnoreAlert(alert: StockV2Alert) {
    const ok = await confirmDanger({
      title: "忽略提醒",
      body: "忽略后该提醒不再出现在待处理列表。可在台账中回看,不会自动恢复。",
      objectName: alert.title || alert.id,
      confirmLabel: "忽略",
    });
    if (ok) await refreshAfter("已忽略提醒", () => changeAlertStatus(alert, "ignore"));
  }

  async function requestResolveAlert(alert: StockV2Alert) {
    const ok = await confirmDanger({
      title: "解决提醒",
      body: "标记为已解决。该提醒将进入历史台账,不再需要处理。",
      objectName: alert.title || alert.id,
      confirmLabel: "解决",
    });
    if (ok) await refreshAfter("已解决提醒", () => changeAlertStatus(alert, "resolve"));
  }

  const hasWatches = watchTotal > 0 || watches.length > 0;

  return (
    <div className="grid gap-4">
      <Panel
        title="盯盘"
        subtitle={`${watchTotal} 个 · 规则命中后产生提醒,不直接生成买卖建议`}
        actions={
          <>
            <Button onClick={() => void fetchWatches()} disabled={watchLoading}>
              <ArrowsClockwise size={14} className="mr-1.5" />
              {watchLoading ? "加载中" : "刷新"}
            </Button>
            <Button tone="primary" onClick={() => setCreateOpen(true)}>
              <Plus size={14} className="mr-1.5" />
              新建盯盘
            </Button>
          </>
        }
      >
        {watchError ? (
          <div className="mb-3">
            <Notice tone="warn">盯盘接口暂不可用:{watchError}</Notice>
          </div>
        ) : null}

        {runResult ? <RunResultSummary result={runResult} onClose={() => setRunResult(null)} /> : null}

        {hasWatches ? (
          <>
            <WatchToolbar
              status={watchStatus}
              portfolio={watchPortfolio}
              keyword={watchKeyword}
              portfolios={portfolios}
              onStatus={(v) => { setWatchStatus(v); setWatchPage(1); }}
              onPortfolio={(v) => { setWatchPortfolio(v); setWatchPage(1); }}
              onKeyword={(v) => { setWatchKeyword(v); setWatchPage(1); }}
            />

            {watches.length === 0 ? (
              <p className="py-6 text-center text-sm text-[var(--muted)]">没有匹配的盯盘,调整筛选条件试试。</p>
            ) : (
              <div className="mt-3 grid gap-2">
                {watches.map((w) => (
                  <WatchRow
                    key={w.id}
                    watch={w}
                    portfolios={portfolios}
                    submitting={submitting}
                    onRun={() => void runWatch(w)}
                    onActivate={() => void refreshAfter("已启动盯盘", () => changeWatchStatus(w, "activate"))}
                    onPause={() => void requestPause(w)}
                    onArchive={() => void requestArchive(w)}
                  />
                ))}
              </div>
            )}

            <Pagination
              loading={watchLoading}
              page={watchPage}
              pageNumbers={watchPageNumbers}
              pageSize={WATCH_PAGE_SIZE}
              total={watchTotal}
              totalPages={watchTotalPages}
              onPage={setWatchPage}
              label="盯盘页码"
            />
          </>
        ) : watchLoading ? (
          <p className="py-6 text-center text-sm text-[var(--muted)]">加载盯盘…</p>
        ) : (
          <WatchEmptyState onCreate={() => setCreateOpen(true)} />
        )}
      </Panel>

      <Panel title="提醒台账" subtitle={`${alertTotal} 条 · 规则命中后留存,确认 / 忽略 / 解决`}>
        {alertError ? (
          <div className="mb-3">
            <Notice tone="warn">提醒接口暂不可用:{alertError}</Notice>
          </div>
        ) : null}

        <AlertToolbar status={alertStatus} onStatus={(v) => { setAlertStatus(v); setAlertPage(1); }} />

        {alertLoading ? (
          <p className="py-6 text-center text-sm text-[var(--muted)]">加载提醒…</p>
        ) : alerts.length === 0 ? (
          <p className="py-6 text-center text-sm text-[var(--muted)]">
            {alertTotal === 0 ? "暂无提醒。运行盯盘后,规则命中将在此产生提醒。" : "没有匹配的提醒。"}
          </p>
        ) : (
          <>
            <div className="mt-3 grid gap-2">
              {alerts.map((a) => (
                <AlertRow
                  key={a.id}
                  alert={a}
                  submitting={submitting}
                  onAck={() => void refreshAfter("已确认提醒", () => changeAlertStatus(a, "ack"))}
                  onIgnore={() => void requestIgnoreAlert(a)}
                  onResolve={() => void requestResolveAlert(a)}
                />
              ))}
            </div>
            <Pagination
              loading={alertLoading}
              page={alertPage}
              pageNumbers={alertPageNumbers}
              pageSize={ALERT_PAGE_SIZE}
              total={alertTotal}
              totalPages={alertTotalPages}
              onPage={setAlertPage}
              label="提醒页码"
            />
          </>
        )}
      </Panel>

      {createOpen ? (
        <CreateWatchDrawer
          portfolios={portfolios}
          actions={actions}
          submitting={submitting}
          onClose={() => setCreateOpen(false)}
          onSubmit={async (input) => {
            setSubmitting(true);
            try {
              await actions.api("/api/stockv2/watches", { method: "POST", body: input });
              actions.setToast("已创建盯盘", "good");
              setCreateOpen(false);
              await fetchWatches();
            } catch (err) {
              actions.setToast(friendlyError(err), "danger");
            } finally {
              setSubmitting(false);
            }
          }}
        />
      ) : null}

      {dangerConfirmDialog}
    </div>
  );
}

// ============================ Watch 列表 ============================

function WatchToolbar({
  status,
  portfolio,
  keyword,
  portfolios,
  onStatus,
  onPortfolio,
  onKeyword,
}: {
  status: string;
  portfolio: string;
  keyword: string;
  portfolios: StockV2Portfolio[];
  onStatus: (v: string) => void;
  onPortfolio: (v: string) => void;
  onKeyword: (v: string) => void;
}) {
  return (
    <div className="grid grid-cols-2 gap-2 sm:grid-cols-3">
      <label className="field">
        <span>状态</span>
        <select value={status} onChange={(e) => onStatus(e.target.value)}>
          <option value="all">全部状态</option>
          <option value="active">盯盘中</option>
          <option value="paused">已暂停</option>
          <option value="archived">已归档</option>
        </select>
      </label>
      <label className="field">
        <span>组合</span>
        <select value={portfolio} onChange={(e) => onPortfolio(e.target.value)}>
          <option value="all">全部组合</option>
          {portfolios.map((p) => (
            <option key={p.id} value={p.id}>
              {p.name}
            </option>
          ))}
        </select>
      </label>
      <label className="field">
        <span>代码 / 名称</span>
        <input type="text" value={keyword} placeholder="例如 302132" onChange={(e) => onKeyword(e.target.value)} />
      </label>
    </div>
  );
}

function WatchRow({
  watch,
  portfolios,
  submitting,
  onRun,
  onActivate,
  onPause,
  onArchive,
}: {
  watch: StockV2Watch;
  portfolios: StockV2Portfolio[];
  submitting: boolean;
  onRun: () => void;
  onActivate: () => void;
  onPause: () => void;
  onArchive: () => void;
}) {
  const portfolio = watch.portfolioId ? portfolios.find((p) => p.id === watch.portfolioId) : null;
  const archived = watch.status === "archived";
  const name = watch.name || watch.symbol || watch.id;
  return (
    <div className="grid grid-cols-[minmax(0,1fr)_auto] items-start gap-3 rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3 transition hover:border-[var(--line-strong)]">
      <div className="min-w-0">
        <div className="flex flex-wrap items-center gap-2">
          <strong className="truncate text-sm">{name}</strong>
          <Pill tone={stockV2WatchStatusTone(watch.status)}>{stockV2WatchStatusLabel(watch.status)}</Pill>
          <Pill tone="neutral">{stockV2WatchSourceLabel(watch.source)}</Pill>
          {watch.scheduleKind ? <span className="text-xs text-[var(--muted)]">· {stockV2WatchScheduleLabel(watch.scheduleKind)}</span> : null}
        </div>
        <div className="mt-1.5 flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-[var(--muted-strong)]">
          {watch.symbol ? (
            <span>
              标的 <span className="font-mono text-[var(--text)]">{watch.symbol}</span>
              {watch.instrumentName ? ` · ${watch.instrumentName}` : ""}
            </span>
          ) : null}
          {watch.portfolioId ? <span>组合 <span className="text-[var(--text)]">{watch.portfolioName || portfolio?.name || watch.portfolioId}</span></span> : null}
          <span>规则 <span className="text-[var(--text)]">{stockV2WatchRuleSummary(watch)}</span></span>
          {typeof watch.cooldownSeconds === "number" ? <span>冷却 {formatDuration(watch.cooldownSeconds)}</span> : null}
        </div>
        <div className="mt-1 flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-[var(--muted)]">
          <span>最近检查 {formatDate(watch.lastCheckedAt) || "—"}</span>
          <span>最近触发 {formatDate(watch.lastTriggeredAt) || "—"}</span>
        </div>
      </div>
      <div className="flex flex-wrap items-start justify-end gap-1">
        {!archived ? (
          <Button onClick={onRun} disabled={submitting} title="立即运行检查">
            <ArrowsClockwise size={12} className="mr-1" />
            运行
          </Button>
        ) : null}
        {!archived && watch.status === "paused" ? (
          <Button onClick={onActivate} disabled={submitting} title="启动">
            <Play size={12} className="mr-1" />
            启动
          </Button>
        ) : null}
        {!archived && watch.status === "active" ? (
          <Button onClick={onPause} disabled={submitting} title="暂停">
            <Pause size={12} className="mr-1" />
            暂停
          </Button>
        ) : null}
        {!archived ? (
          <Button tone="danger" onClick={onArchive} disabled={submitting} title="归档">
            <Archive size={12} className="mr-1" />
            归档
          </Button>
        ) : (
          <span className="text-xs text-[var(--muted)]">已归档</span>
        )}
      </div>
    </div>
  );
}

function WatchEmptyState({ onCreate }: { onCreate: () => void }) {
  return (
    <div className="py-8 text-center">
      <p className="text-sm text-[var(--muted)]">还没有盯盘对象</p>
      <p className="mx-auto mt-1 max-w-md text-xs leading-relaxed text-[var(--muted-strong)]">
        为股票或组合建立确定性触发规则(价格突破、涨跌幅、数据过期、权重过高),命中后产生可追踪提醒。
      </p>
      <Button tone="primary" className="mt-3" onClick={onCreate}>
        <Plus size={14} className="mr-1.5" />
        新建盯盘
      </Button>
    </div>
  );
}

// ============================ Alert 台账 ============================

function AlertToolbar({ status, onStatus }: { status: string; onStatus: (v: string) => void }) {
  return (
    <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
      <label className="field">
        <span>状态</span>
        <select value={status} onChange={(e) => onStatus(e.target.value)}>
          <option value="all">全部状态</option>
          <option value="open">待处理</option>
          <option value="acknowledged">已确认</option>
          <option value="ignored">已忽略</option>
          <option value="resolved">已解决</option>
        </select>
      </label>
    </div>
  );
}

function AlertRow({
  alert,
  submitting,
  onAck,
  onIgnore,
  onResolve,
}: {
  alert: StockV2Alert;
  submitting: boolean;
  onAck: () => void;
  onIgnore: () => void;
  onResolve: () => void;
}) {
  const open = alert.status === "open";
  return (
    <div className="grid grid-cols-[minmax(0,1fr)_auto] items-start gap-3 rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3">
      <div className="min-w-0">
        <div className="flex flex-wrap items-center gap-2">
          <Pill tone={stockV2AlertStatusTone(alert.status)}>{stockV2AlertStatusLabel(alert.status)}</Pill>
          <Pill tone={stockV2AlertLevelTone(alert.level)}>{stockV2AlertLevelLabel(alert.level)}</Pill>
          <strong className="truncate text-sm">{alert.title || "(无标题)"}</strong>
        </div>
        {alert.summary ? <p className="mt-1 break-words text-xs leading-relaxed text-[var(--muted-strong)]">{alert.summary}</p> : null}
        <div className="mt-1 flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-[var(--muted)]">
          {alert.watchName ? <span>盯盘 <span className="text-[var(--muted-strong)]">{alert.watchName}</span></span> : null}
          {alert.symbol ? <span className="font-mono">{alert.symbol}</span> : null}
          <span>触发 {formatDate(alert.triggeredAt || alert.createdAt) || "—"}</span>
        </div>
      </div>
      <div className="flex flex-wrap items-start justify-end gap-1">
        {open ? (
          <Button onClick={onAck} disabled={submitting} title="确认">
            <Check size={12} className="mr-1" />
            确认
          </Button>
        ) : null}
        {alert.status === "open" || alert.status === "acknowledged" ? (
          <>
            <Button onClick={onResolve} disabled={submitting} title="解决">
              <CheckCircle size={12} className="mr-1" />
              解决
            </Button>
            <Button tone="danger" onClick={onIgnore} disabled={submitting} title="忽略">
              <X size={12} className="mr-1" />
              忽略
            </Button>
          </>
        ) : null}
      </div>
    </div>
  );
}

// ============================ 运行结果摘要 ============================

function RunResultSummary({ result, onClose }: { result: StockV2WatchRunResult; onClose: () => void }) {
  const totals = result.totals || {};
  const cells: Array<{ key: string; label: string; value: number; tone: "good" | "warn" | "danger" | "neutral" }> = [
    { key: "matched", label: "命中", value: totals.matched ?? 0, tone: stockV2WatchRunStatusTone("matched") },
    { key: "not_matched", label: "未命中", value: totals.notMatched ?? 0, tone: stockV2WatchRunStatusTone("not_matched") },
    { key: "skipped", label: "跳过", value: totals.skipped ?? 0, tone: stockV2WatchRunStatusTone("skipped") },
    { key: "degraded", label: "降级", value: totals.degraded ?? 0, tone: stockV2WatchRunStatusTone("degraded") },
  ];
  return (
    <div className="mb-3 rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3">
      <div className="flex items-center justify-between">
        <span className="text-xs font-medium text-[var(--muted-strong)]">最近运行结果</span>
        <Button onClick={onClose} title="关闭">
          <X size={12} />
        </Button>
      </div>
      <div className="mt-2 flex flex-wrap gap-2">
        {cells.map((c) => (
          <Pill key={c.key} tone={c.tone}>
            {c.label} {c.value}
          </Pill>
        ))}
      </div>
      {result.alerts?.length ? (
        <p className="mt-2 text-xs text-[var(--muted)]">产生 {result.alerts.length} 条新提醒,见下方台账。</p>
      ) : null}
      {result.note ? <p className="mt-1 break-words text-xs text-[var(--muted)]">{result.note}</p> : null}
    </div>
  );
}

// ============================ 创建 drawer ============================

function CreateWatchDrawer({
  portfolios,
  actions,
  submitting,
  onClose,
  onSubmit,
}: {
  portfolios: StockV2Portfolio[];
  actions: AppActions;
  submitting: boolean;
  onClose: () => void;
  onSubmit: (input: StockV2WatchInput) => Promise<void>;
}) {
  const [name, setName] = useState("");
  const [symbolRef, setSymbolRef] = useState<SymbolRef>({ symbol: "" });
  const [portfolioId, setPortfolioId] = useState("");
  const [triggerKind, setTriggerKind] = useState<StockV2WatchTriggerKind | string>("price_above");
  const [threshold, setThreshold] = useState("");
  const [cooldown, setCooldown] = useState(String(DEFAULT_COOLDOWN));
  const [scheduleKind, setScheduleKind] = useState<StockV2WatchScheduleKind | string>("continuous");

  const needsPortfolio = triggerKind === "portfolio_weight_high";
  const hasSubject = symbolRef.symbol.trim().length > 0 || portfolioId.length > 0;
  const canSubmit = hasSubject && (!needsPortfolio || portfolioId.length > 0) && !submitting;

  function buildInput(): StockV2WatchInput {
    return {
      name: name.trim() || undefined,
      symbol: symbolRef.symbol.trim() || undefined,
      market: symbolRef.market,
      portfolioId: portfolioId || undefined,
      triggerKind,
      threshold: numOrUndef(threshold),
      cooldownSeconds: numOrUndef(cooldown),
      scheduleKind,
    };
  }

  return (
    <Drawer title="新建盯盘" subtitle="选择标的或组合,设定确定性触发规则" onClose={onClose} width={480}>
      <div className="grid gap-3">
        <Field label="名称(可选)">
          <input type="text" value={name} placeholder="留空将按标的与规则自动生成" onChange={(e) => setName(e.target.value)} />
        </Field>

        <Field label="标的股票" help={needsPortfolio ? "该规则作用于组合,标的可不填。" : "单票规则需选择标的。"}>
          <SymbolPicker actions={actions} value={symbolRef} onChange={setSymbolRef} />
        </Field>

        <Field label="绑定组合(可选)">
          <select value={portfolioId} onChange={(e) => setPortfolioId(e.target.value)}>
            <option value="">{portfolios.length ? "不绑定组合" : "暂无组合"}</option>
            {portfolios.map((p) => (
              <option key={p.id} value={p.id}>
                {p.name}
              </option>
            ))}
          </select>
        </Field>

        <div className="grid grid-cols-2 gap-3">
          <Field label="规则类型">
            <select value={triggerKind} onChange={(e) => setTriggerKind(e.target.value)}>
              <option value="price_above">价格突破</option>
              <option value="price_below">价格跌破</option>
              <option value="pct_change_up">涨幅超限</option>
              <option value="pct_change_down">跌幅超限</option>
              <option value="data_stale">数据过期</option>
              <option value="portfolio_weight_high">组合权重过高</option>
            </select>
          </Field>
          <Field label="阈值">
            <input
              type="number"
              step="0.01"
              value={threshold}
              placeholder={thresholdPlaceholder(triggerKind)}
              onChange={(e) => setThreshold(e.target.value)}
            />
          </Field>
        </div>

        <div className="grid grid-cols-2 gap-3">
          <Field label="冷却时间(秒)" help="同一规则命中后的最短重复提醒间隔。">
            <input type="number" min="0" step="1" value={cooldown} onChange={(e) => setCooldown(e.target.value)} />
          </Field>
          <Field label="检查节奏">
            <select value={scheduleKind} onChange={(e) => setScheduleKind(e.target.value)}>
              <option value="continuous">持续</option>
              <option value="market_open">盘中</option>
              <option value="daily">每日</option>
              <option value="hourly">每小时</option>
            </select>
          </Field>
        </div>

        <p className="text-xs leading-relaxed text-[var(--muted)]">
          盯盘只负责确定性规则判断与提醒。是否买卖由后续 Review 决定,不会直接改持仓。
        </p>

        <div className="flex justify-end gap-2 border-t border-[var(--line)] pt-3">
          <Button onClick={onClose}>取消</Button>
          <Button tone="primary" disabled={!canSubmit} onClick={() => void onSubmit(buildInput())}>
            {submitting ? "创建中…" : "创建盯盘"}
          </Button>
        </div>
      </div>
    </Drawer>
  );
}

// ============================ 标的搜索 ============================

function SymbolPicker({
  actions,
  value,
  onChange,
}: {
  actions: AppActions;
  value: SymbolRef;
  onChange: (ref: SymbolRef) => void;
}) {
  const [query, setQuery] = useState(value.symbol && value.name ? `${value.symbol} · ${value.name}` : value.symbol || "");
  const [results, setResults] = useState<StockV2Instrument[]>([]);
  const [open, setOpen] = useState(false);
  const [searching, setSearching] = useState(false);
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const wrapRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    function onClick(e: MouseEvent) {
      if (wrapRef.current && !wrapRef.current.contains(e.target as Node)) setOpen(false);
    }
    document.addEventListener("mousedown", onClick);
    return () => document.removeEventListener("mousedown", onClick);
  }, []);

  useEffect(() => {
    if (timerRef.current) clearTimeout(timerRef.current);
    if (!query.trim()) {
      setResults([]);
      return;
    }
    timerRef.current = setTimeout(async () => {
      setSearching(true);
      try {
        const res = await actions.api<{ items: StockV2Instrument[] }>(
          `/api/stockv2/instruments/search?q=${encodeURIComponent(query)}&limit=20`,
        );
        setResults(res.items || []);
        setOpen(true);
      } catch {
        setResults([]);
      } finally {
        setSearching(false);
      }
    }, 200);
    return () => {
      if (timerRef.current) clearTimeout(timerRef.current);
    };
  }, [query, actions]);

  function pick(inst: StockV2Instrument) {
    onChange({ symbol: inst.symbol, market: inst.market, name: inst.name });
    setQuery(`${inst.symbol} · ${inst.name || ""}`);
    setOpen(false);
  }

  return (
    <div className="relative" ref={wrapRef}>
      <div className="relative">
        <MagnifyingGlass size={14} className="absolute left-2.5 top-1/2 -translate-y-1/2 text-[var(--muted)]" />
        <input
          type="text"
          className="w-full rounded border border-[var(--line)] bg-[var(--surface)] py-2 pl-8 pr-3 text-sm text-[var(--text)] focus:border-[var(--accent)] focus:outline-none"
          placeholder="输入代码或名称搜索"
          value={query}
          onChange={(e) => {
            setQuery(e.target.value);
            setOpen(true);
          }}
          onFocus={() => {
            if (results.length) setOpen(true);
          }}
        />
      </div>
      {open ? (
        <div className="absolute left-0 right-0 top-full z-10 mt-1 max-h-64 overflow-y-auto rounded-lg border border-[var(--line)] bg-[var(--surface)] shadow-[var(--shadow)]">
          {searching ? (
            <div className="px-3 py-2 text-xs text-[var(--muted)]">搜索中…</div>
          ) : results.length === 0 ? (
            <div className="px-3 py-2 text-xs text-[var(--muted)]">{query ? "未找到匹配的股票" : "输入关键词开始搜索"}</div>
          ) : (
            results.map((inst) => (
              <button
                key={inst.id}
                type="button"
                onClick={() => pick(inst)}
                className="flex w-full items-center justify-between px-3 py-2 text-left text-sm hover:bg-[var(--surface-soft)]"
              >
                <span className="font-mono">{inst.symbol}</span>
                <span className="mx-2 min-w-0 truncate text-[var(--muted)]">{inst.name}</span>
                <Pill tone="neutral" className="text-xs">
                  {inst.market === "SH" ? "沪" : inst.market === "SZ" ? "深" : inst.market === "BJ" ? "北" : inst.market}
                </Pill>
              </button>
            ))
          )}
        </div>
      ) : null}
    </div>
  );
}

// ============================ 分页 ============================

function Pagination({
  loading,
  page,
  pageNumbers,
  pageSize,
  total,
  totalPages,
  onPage,
  label,
}: {
  loading: boolean;
  page: number;
  pageNumbers: Array<number | "ellipsis">;
  pageSize: number;
  total: number;
  totalPages: number;
  onPage: (page: number) => void;
  label: string;
}) {
  if (total <= pageSize) return null;
  const start = (page - 1) * pageSize + 1;
  const end = Math.min(total, page * pageSize);
  return (
    <div className="mt-4 flex flex-wrap items-center justify-between gap-3 border-t border-[var(--line)] pt-3 text-xs">
      <span className="text-[var(--muted)]">
        第 {page} / {totalPages} 页 · {start}-{end} / {total}
      </span>
      <div className="flex flex-wrap items-center gap-1.5">
        <Button disabled={loading || page <= 1} onClick={() => onPage(Math.max(1, page - 1))}>
          上一页
        </Button>
        {pageNumbers.map((item, index) =>
          item === "ellipsis" ? (
            <span className="px-2 text-[var(--muted)]" key={`${label}-gap-${index}`}>...</span>
          ) : (
            <Button
              className={item === page ? "border-[var(--accent)] text-[var(--accent)]" : ""}
              disabled={loading}
              key={item}
              onClick={() => onPage(item)}
            >
              {item}
            </Button>
          ),
        )}
        <Button disabled={loading || page >= totalPages} onClick={() => onPage(Math.min(totalPages, page + 1))}>
          下一页
        </Button>
        <select
          aria-label={`选择${label}`}
          className="select h-9 w-24 text-xs"
          disabled={loading}
          onChange={(event) => onPage(Number(event.target.value))}
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
  );
}

// ============================ helpers ============================

function paginationWindow(page: number, totalPages: number): Array<number | "ellipsis"> {
  if (totalPages <= 7) {
    return Array.from({ length: totalPages }, (_, idx) => idx + 1);
  }
  const pages = new Set<number>([1, totalPages, page, page - 1, page + 1]);
  if (page <= 3) {
    pages.add(2);
    pages.add(3);
    pages.add(4);
  }
  if (page >= totalPages - 2) {
    pages.add(totalPages - 1);
    pages.add(totalPages - 2);
    pages.add(totalPages - 3);
  }
  const sorted = Array.from(pages)
    .filter((item) => item >= 1 && item <= totalPages)
    .sort((a, b) => a - b);
  const result: Array<number | "ellipsis"> = [];
  sorted.forEach((item) => {
    const previous = result[result.length - 1];
    if (typeof previous === "number" && item - previous > 1) {
      result.push("ellipsis");
    }
    result.push(item);
  });
  return result;
}

function numOrUndef(value: string): number | undefined {
  if (value.trim() === "") return undefined;
  const n = Number(value);
  return Number.isFinite(n) ? n : undefined;
}

function thresholdPlaceholder(kind: string): string {
  if (stockV2WatchTriggerIsPercent(kind)) return "例如 5 (%)";
  if (kind === "price_above" || kind === "price_below") return "例如 20.00";
  if (kind === "data_stale") return "留空使用默认";
  return "";
}

function formatDuration(seconds: number): string {
  if (seconds <= 0) return "无";
  if (seconds < 60) return `${seconds}s`;
  if (seconds < 3600) return `${Math.round(seconds / 60)}min`;
  return `${Math.round(seconds / 3600)}h`;
}
