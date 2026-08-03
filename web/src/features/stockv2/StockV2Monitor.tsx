import { ArrowsClockwise, Check, CheckCircle, MagnifyingGlass, Pencil, Power, ShieldCheck, X } from "@phosphor-icons/react";
import { useEffect, useMemo, useRef, useState } from "react";
import type { AppActions } from "../../app/App";
import type {
  StockV2AgentExecutionDetail,
  StockV2AgentListResponse,
  StockV2Alert,
  StockV2AlertListResponse,
  StockV2MonitorHit,
  StockV2MonitorHitListResponse,
  StockV2MonitorReviewPipeline,
  StockV2MonitorRun,
  StockV2MonitorRunListResponse,
  StockV2MonitorTask,
  StockV2MonitorTaskConfigInput,
  StockV2MonitorTaskListResponse,
  StockV2QuoteRefreshStateResponse,
  StockV2QuoteRefreshStatus,
  StockV2QuoteRefreshTaskState,
} from "../../app/types";
import { friendlyError } from "../../api/client";
import { Button, CollapsibleSection, Drawer, Field, Notice, Pill, useDangerConfirm } from "../../components/ui";
import { buildQueryHref } from "../../hooks/useQueryParamState";
import {
  StockV2AgentExecutionLedgerSection,
  StockV2AgentExecutionSummaryList,
  StockV2AgentRunDetailDrawer,
} from "./StockV2AgentExecutionLedger";
import { StockV2ReviewDrawer } from "./StockV2ReviewDrawer";
import {
  formatDate,
  stockV2AgentRunStatusLabel,
  stockV2AgentRunStatusTone,
  stockV2AlertLevelLabel,
  stockV2AlertLevelTone,
  stockV2AlertStatusLabel,
  stockV2AlertStatusTone,
  stockV2MonitorAgentStateLabel,
  stockV2MonitorCategoryLabel,
  stockV2MonitorHitStatusLabel,
  stockV2MonitorHitStatusTone,
  stockV2MonitorRunStatusLabel,
  stockV2MonitorRunStatusTone,
  stockV2MonitorTaskTypeLabel,
  stockV2ReviewStatusLabel,
  stockV2ReviewStatusTone,
} from "../../domain/labels";

// 监控与任务:盯盘不再是用户创建的对象,而是系统固化的后台监控。
// 用户只配置开关/周期/范围/敏感度/冷却/Agent,观察运行/命中/提醒。
// 默认折叠,但摘要(运行中/失败/启用/open alert)始终可见。

const RUN_PAGE_SIZE = 10;
const ALERT_PAGE_SIZE = 10;

export function StockV2Monitor({ actions }: { actions: AppActions }) {
  const [tasks, setTasks] = useState<StockV2MonitorTask[]>([]);
  const [tasksLoading, setTasksLoading] = useState(true);
  const [tasksError, setTasksError] = useState<string | null>(null);

  const [runs, setRuns] = useState<StockV2MonitorRun[]>([]);
  const [runsTotal, setRunsTotal] = useState(0);
  const [runsPage, setRunsPage] = useState(1);
  const [runsLoading, setRunsLoading] = useState(false);

  const [hitsByRunId, setHitsByRunId] = useState<Record<string, StockV2MonitorHit[]>>({});
  const [agentDetailsByRunId, setAgentDetailsByRunId] = useState<Record<string, StockV2AgentExecutionDetail[]>>({});
  const [selectedRun, setSelectedRun] = useState<StockV2MonitorRun | null>(null);
  const [selectedRunHits, setSelectedRunHits] = useState<StockV2MonitorHit[]>([]);
  const [selectedRunHitsLoading, setSelectedRunHitsLoading] = useState(false);
  const [selectedRunAgentDetails, setSelectedRunAgentDetails] = useState<StockV2AgentExecutionDetail[]>([]);
  const [selectedRunAgentLoading, setSelectedRunAgentLoading] = useState(false);
  const [agentDetailRunId, setAgentDetailRunId] = useState<string | null>(null);

  const [alerts, setAlerts] = useState<StockV2Alert[]>([]);
  const [alertsTotal, setAlertsTotal] = useState(0);
  const [openAlertsTotal, setOpenAlertsTotal] = useState(0);
  const [alertsPage, setAlertsPage] = useState(1);
  const [alertsLoading, setAlertsLoading] = useState(false);
  const [selectedAlert, setSelectedAlert] = useState<StockV2Alert | null>(null);

  const [quoteRefreshState, setQuoteRefreshState] = useState<StockV2QuoteRefreshStateResponse | null>(null);
  const [quoteRefreshLoading, setQuoteRefreshLoading] = useState(false);

  const [editTask, setEditTask] = useState<StockV2MonitorTask | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [reviewHitId, setReviewHitId] = useState<string | null>(null);
  const { confirmDanger, dangerConfirmDialog } = useDangerConfirm();
  const initialRunsLoaded = useRef(false);
  const initialAlertsLoaded = useRef(false);

  async function fetchTasks() {
    setTasksLoading(true);
    setTasksError(null);
    try {
      const res = await actions.api<StockV2MonitorTaskListResponse>("/api/stockv2/monitor/tasks");
      setTasks(res.items || []);
    } catch (err) {
      setTasksError(friendlyError(err));
      setTasks([]);
    } finally {
      setTasksLoading(false);
    }
  }

  async function fetchRuns(page = runsPage) {
    setRunsLoading(true);
    try {
      const params = new URLSearchParams({ limit: String(RUN_PAGE_SIZE), offset: String((Math.max(1, page) - 1) * RUN_PAGE_SIZE) });
      const res = await actions.api<StockV2MonitorRunListResponse>(`/api/stockv2/monitor/runs?${params}`);
      const items = res.items || [];
      setRuns(items);
      setRunsTotal(res.total ?? res.items?.length ?? 0);
      void fetchHitsForRuns(items);
      void fetchAgentDetailsForRuns(items);
    } catch {
      setRuns([]);
      setRunsTotal(0);
    } finally {
      setRunsLoading(false);
    }
  }

  async function fetchHitsForRuns(runItems: StockV2MonitorRun[]) {
    const ids = runItems.map((run) => run.id).filter(Boolean);
    if (ids.length === 0) return;
    try {
      const pairs = await Promise.all(ids.map(async (runId) => [runId, await fetchRunHits(runId, 50)] as const));
      setHitsByRunId((current) => {
        const next = { ...current };
        pairs.forEach(([runId, items]) => {
          next[runId] = items;
        });
        return next;
      });
    } catch {
      // 单条 run 的命中详情只是历史增强信息,失败不影响主列表展示。
    }
  }

  async function fetchRunHits(runId: string, limit = 100) {
    const params = new URLSearchParams({ runId, limit: String(limit), offset: "0" });
    const res = await actions.api<StockV2MonitorHitListResponse>(`/api/stockv2/monitor/hits?${params}`);
    return res.items || [];
  }

  async function fetchAgentDetailsForRuns(runItems: StockV2MonitorRun[]) {
    const ids = runItems.map((run) => run.id).filter(Boolean);
    if (ids.length === 0) return;
    try {
      const pairs = await Promise.all(ids.map(async (runId) => [runId, await fetchRunAgentDetails(runId)] as const));
      setAgentDetailsByRunId((current) => {
        const next = { ...current };
        pairs.forEach(([runId, items]) => {
          next[runId] = items;
        });
        return next;
      });
    } catch {
      // Agent 详情是监控历史增强信息,失败不影响监控任务主列表。
    }
  }

  async function fetchRunAgentDetails(runId: string) {
    const res = await actions.api<StockV2AgentListResponse<StockV2AgentExecutionDetail>>(
      `/api/stockv2/monitor/runs/${runId}/agent-runs`,
    );
    return res.items || [];
  }

  async function fetchAlerts(page = alertsPage) {
    setAlertsLoading(true);
    try {
      const params = new URLSearchParams({ limit: String(ALERT_PAGE_SIZE), offset: String((Math.max(1, page) - 1) * ALERT_PAGE_SIZE) });
      const openParams = new URLSearchParams({ status: "open", limit: "1", offset: "0" });
      const [res, openRes] = await Promise.all([
        actions.api<StockV2AlertListResponse>(`/api/stockv2/alerts?${params}`),
        actions.api<StockV2AlertListResponse>(`/api/stockv2/alerts?${openParams}`),
      ]);
      setAlerts(res.items || []);
      setAlertsTotal(res.total ?? res.items?.length ?? 0);
      setOpenAlertsTotal(openRes.total ?? openRes.items?.length ?? 0);
    } catch {
      setAlerts([]);
      setAlertsTotal(0);
      setOpenAlertsTotal(0);
    } finally {
      setAlertsLoading(false);
    }
  }

  async function fetchQuoteRefreshState() {
    setQuoteRefreshLoading(true);
    try {
      const res = await actions.api<StockV2QuoteRefreshStateResponse>("/api/stockv2/quotes/refresh-state?limit=20");
      setQuoteRefreshState(res);
    } catch {
      setQuoteRefreshState(null);
    } finally {
      setQuoteRefreshLoading(false);
    }
  }

  useEffect(() => {
    void fetchTasks();
    void fetchRuns();
    void fetchAlerts();
    void fetchQuoteRefreshState();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    if (!initialRunsLoaded.current) {
      initialRunsLoaded.current = true;
      return;
    }
    void fetchRuns(runsPage);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [runsPage]);
  useEffect(() => {
    if (!initialAlertsLoaded.current) {
      initialAlertsLoaded.current = true;
      return;
    }
    void fetchAlerts(alertsPage);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [alertsPage]);

  const runsTotalPages = Math.max(1, Math.ceil(runsTotal / RUN_PAGE_SIZE));
  const runsPageNumbers = useMemo(() => paginationWindow(runsPage, runsTotalPages), [runsPage, runsTotalPages]);
  const alertsTotalPages = Math.max(1, Math.ceil(alertsTotal / ALERT_PAGE_SIZE));
  const alertsPageNumbers = useMemo(() => paginationWindow(alertsPage, alertsTotalPages), [alertsPage, alertsTotalPages]);

  const runningCount = tasks.filter((t) => t.latestRun?.status === "running").length;
  const failedCount = tasks.filter((t) => t.latestRun?.status === "failed").length;
  const enabledCount = tasks.filter((t) => t.config?.enabled).length;
  const openAlertCount = openAlertsTotal;

  async function runTask(taskType: string) {
    setSubmitting(true);
    try {
      await actions.api(`/api/stockv2/monitor/tasks/${taskType}/run`, { method: "POST" });
      actions.setToast("已触发监控任务", "good");
      await Promise.all([fetchTasks(), fetchRuns(), fetchAlerts(), fetchQuoteRefreshState()]);
    } catch (err) {
      actions.setToast(friendlyError(err), "danger");
    } finally {
      setSubmitting(false);
    }
  }

  async function toggleTaskEnabled(task: StockV2MonitorTask) {
    setSubmitting(true);
    try {
      await actions.api(`/api/stockv2/monitor/tasks/${task.definition.taskType}/config`, {
        method: "PUT",
        body: { enabled: !task.config?.enabled } as StockV2MonitorTaskConfigInput,
      });
      actions.setToast(task.config?.enabled ? "已暂停监控任务" : "已启用监控任务", "good");
      await fetchTasks();
    } catch (err) {
      actions.setToast(friendlyError(err), "danger");
    } finally {
      setSubmitting(false);
    }
  }

  async function saveTaskConfig(taskType: string, input: StockV2MonitorTaskConfigInput) {
    setSubmitting(true);
    try {
      await actions.api(`/api/stockv2/monitor/tasks/${taskType}/config`, { method: "PUT", body: input });
      actions.setToast("已保存监控任务配置", "good");
      setEditTask(null);
      await fetchTasks();
    } catch (err) {
      actions.setToast(friendlyError(err), "danger");
    } finally {
      setSubmitting(false);
    }
  }

  async function changeAlertStatus(alert: StockV2Alert, action: "ack" | "ignore" | "resolve") {
    await actions.api(`/api/stockv2/alerts/${alert.id}/${action}`, { method: "POST" });
  }

  async function ackAlert(alert: StockV2Alert) {
    setSubmitting(true);
    try {
      await changeAlertStatus(alert, "ack");
      actions.setToast("已确认提醒", "good");
      await fetchAlerts();
    } catch (err) {
      actions.setToast(friendlyError(err), "danger");
    } finally {
      setSubmitting(false);
    }
  }

  async function ignoreAlert(alert: StockV2Alert) {
    const ok = await confirmDanger({
      title: "忽略提醒",
      body: "忽略后该提醒不再出现在待处理列表,可在台账回看,不会自动恢复。",
      objectName: alert.title || alert.id,
      confirmLabel: "忽略",
    });
    if (!ok) return;
    setSubmitting(true);
    try {
      await changeAlertStatus(alert, "ignore");
      actions.setToast("已忽略提醒", "good");
      await fetchAlerts();
    } catch (err) {
      actions.setToast(friendlyError(err), "danger");
    } finally {
      setSubmitting(false);
    }
  }

  async function resolveAlert(alert: StockV2Alert) {
    const ok = await confirmDanger({
      title: "解决提醒",
      body: "标记为已解决,进入历史台账,不再需要处理。",
      objectName: alert.title || alert.id,
      confirmLabel: "解决",
    });
    if (!ok) return;
    setSubmitting(true);
    try {
      await changeAlertStatus(alert, "resolve");
      actions.setToast("已解决提醒", "good");
      await fetchAlerts();
    } catch (err) {
      actions.setToast(friendlyError(err), "danger");
    } finally {
      setSubmitting(false);
    }
  }

  async function openRunDetail(run: StockV2MonitorRun) {
    setSelectedRun(run);
    setSelectedRunHits(hitsByRunId[run.id] || []);
    setSelectedRunAgentDetails(agentDetailsByRunId[run.id] || []);
    setSelectedRunHitsLoading(true);
    setSelectedRunAgentLoading(true);
    try {
      const [hits, agentDetails] = await Promise.all([fetchRunHits(run.id, 100), fetchRunAgentDetails(run.id)]);
      setSelectedRunHits(hits);
      setSelectedRunAgentDetails(agentDetails);
      setHitsByRunId((current) => ({ ...current, [run.id]: hits }));
      setAgentDetailsByRunId((current) => ({ ...current, [run.id]: agentDetails }));
    } catch (err) {
      actions.setToast(`加载监控详情失败:${friendlyError(err)}`, "danger");
    } finally {
      setSelectedRunHitsLoading(false);
      setSelectedRunAgentLoading(false);
    }
  }

  return (
    <div className="grid gap-4">
      {tasksError ? (
        <Notice tone="warn">监控接口暂不可用:{tasksError}</Notice>
      ) : null}

      <CollapsibleSection
        title="提醒台账"
        subtitle={alertsTotal > 0 ? `${alertsTotal} 条提醒 · 待处理 ${openAlertCount} · 只放需要用户处理的监控结果` : "暂无需要处理的提醒"}
        defaultOpen={false}
      >
        {alertsLoading ? (
          <p className="text-sm text-[var(--muted)]">加载提醒…</p>
        ) : alerts.length === 0 ? (
          <p className="text-sm text-[var(--muted)]">暂无提醒。命中经(可选)Agent 复核后,需要用户关注的结果会进入台账。</p>
        ) : (
          <>
            <div className="grid gap-2">
              {alerts.map((alert) => (
                <AlertRow
                  key={alert.id}
                  alert={alert}
                  submitting={submitting}
                  onOpen={() => setSelectedAlert(alert)}
                  onAck={() => void ackAlert(alert)}
                  onIgnore={() => void ignoreAlert(alert)}
                  onResolve={() => void resolveAlert(alert)}
                />
              ))}
            </div>
            <Pagination
              loading={alertsLoading}
              page={alertsPage}
              pageNumbers={alertsPageNumbers}
              pageSize={ALERT_PAGE_SIZE}
              total={alertsTotal}
              totalPages={alertsTotalPages}
              onPage={setAlertsPage}
              label="提醒页码"
            />
          </>
        )}
      </CollapsibleSection>

      <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
        <SummaryCell label="启用任务" value={tasksLoading ? "-" : String(enabledCount)} tone={enabledCount > 0 ? "good" : "neutral"} />
        <SummaryCell label="运行中" value={tasksLoading ? "-" : String(runningCount)} tone={runningCount > 0 ? "warn" : "neutral"} />
        <SummaryCell label="最近失败" value={tasksLoading ? "-" : String(failedCount)} tone={failedCount > 0 ? "danger" : "neutral"} />
        <SummaryCell label="Open Alert" value={String(openAlertCount)} tone={openAlertCount > 0 ? "warn" : "neutral"} />
      </div>

      <CollapsibleSection
        title="监控任务配置"
        subtitle={`${enabledCount}/${tasks.length} 启用 · 只配置系统内置监控任务的开关、周期、范围、敏感度、冷却和 Agent doublecheck`}
        defaultOpen={false}
      >
        {tasksLoading ? (
          <p className="text-sm text-[var(--muted)]">加载监控任务…</p>
        ) : tasks.length === 0 ? (
          <p className="text-sm text-[var(--muted)]">暂无监控任务(接口可能未就绪)。</p>
        ) : (
          <div className="grid gap-2">
            {tasks.map((task) => (
              <MonitorTaskRow
                key={task.definition.taskType}
                task={task}
                submitting={submitting}
                onRun={() => void runTask(task.definition.taskType)}
                onToggle={() => void toggleTaskEnabled(task)}
                onEdit={() => setEditTask(task)}
              />
            ))}
          </div>
        )}
      </CollapsibleSection>

      <CollapsibleSection
        title="最新行情刷新状态"
        subtitle={quoteRefreshSubtitle(quoteRefreshState?.state)}
        defaultOpen={false}
      >
        <QuoteRefreshStatePanel loading={quoteRefreshLoading} state={quoteRefreshState?.state} items={quoteRefreshState?.items || []} />
      </CollapsibleSection>

      <CollapsibleSection
        title="监控任务历史"
        subtitle={runsTotal > 0 ? `${runsTotal} 次监控运行 · 展示任务类型、候选命中、Agent doublecheck 状态和影响对象` : "暂无监控运行"}
        defaultOpen={false}
      >
        {runsLoading ? (
          <p className="text-sm text-[var(--muted)]">加载监控任务历史…</p>
        ) : runs.length === 0 ? (
          <p className="text-sm text-[var(--muted)]">暂无监控运行。启用或手动运行监控任务后,历史会出现在这里。</p>
        ) : (
          <>
            <div className="grid gap-2">
              {runs.map((run) => (
                <MonitorRunRow
                  agentDetails={agentDetailsByRunId[run.id] || []}
                  hits={hitsByRunId[run.id] || []}
                  key={run.id}
                  onOpen={() => void openRunDetail(run)}
                  run={run}
                />
              ))}
            </div>
            <Pagination
              loading={runsLoading}
              page={runsPage}
              pageNumbers={runsPageNumbers}
              pageSize={RUN_PAGE_SIZE}
              total={runsTotal}
              totalPages={runsTotalPages}
              onPage={setRunsPage}
              label="运行页码"
            />
          </>
        )}
      </CollapsibleSection>

      <StockV2AgentExecutionLedgerSection actions={actions} />

      {editTask ? (
        <TaskConfigDrawer
          task={editTask}
          submitting={submitting}
          onClose={() => setEditTask(null)}
          onSubmit={(input) => saveTaskConfig(editTask.definition.taskType, input)}
        />
      ) : null}

      {selectedRun ? (
        <MonitorRunDrawer
          hits={selectedRunHits}
          agentDetails={selectedRunAgentDetails}
          agentLoading={selectedRunAgentLoading}
          loading={selectedRunHitsLoading}
          onClose={() => {
            setSelectedRun(null);
            setSelectedRunHits([]);
            setSelectedRunAgentDetails([]);
          }}
          onOpenAgentRun={(runId) => setAgentDetailRunId(runId)}
          onOpenReview={(hitId) => setReviewHitId(hitId)}
          run={selectedRun}
        />
      ) : null}

      {reviewHitId ? (
        <StockV2ReviewDrawer actions={actions} hitId={reviewHitId} onClose={() => setReviewHitId(null)} />
      ) : null}

      {selectedAlert ? (
        <AlertDrawer
          alert={selectedAlert}
          onClose={() => setSelectedAlert(null)}
          onOpenAgentRun={(runId) => setAgentDetailRunId(runId)}
          onOpenReview={(hitId) => setReviewHitId(hitId)}
        />
      ) : null}

      {agentDetailRunId ? (
        <StockV2AgentRunDetailDrawer actions={actions} runId={agentDetailRunId} onClose={() => setAgentDetailRunId(null)} />
      ) : null}

      {dangerConfirmDialog}
    </div>
  );
}

function SummaryCell({ label, value, tone }: { label: string; value: string; tone: "neutral" | "good" | "warn" | "danger" }) {
  return (
    <div className="rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3">
      <span className="block text-xs text-[var(--muted)]">{label}</span>
      <Pill tone={tone} className="mt-1.5 text-sm">
        {value}
      </Pill>
    </div>
  );
}

function QuoteRefreshStatePanel({
  loading,
  state,
  items,
}: {
  loading: boolean;
  state?: StockV2QuoteRefreshTaskState;
  items: StockV2QuoteRefreshStatus[];
}) {
  if (loading) {
    return <p className="text-sm text-[var(--muted)]">加载行情刷新状态…</p>;
  }
  const status = state?.status || "idle";
  return (
    <div className="grid gap-3">
      <div className="grid grid-cols-4 gap-2">
        <SummaryCell label="任务状态" value={quoteRefreshTaskStatusLabel(status)} tone={quoteRefreshTaskStatusTone(status)} />
        <SummaryCell label="扫描股票" value={String(state?.scannedCount ?? 0)} tone="neutral" />
        <SummaryCell label="成功" value={String(state?.successCount ?? 0)} tone="good" />
        <SummaryCell label="失败" value={String(state?.failedCount ?? 0)} tone={(state?.failedCount ?? 0) > 0 ? "danger" : "neutral"} />
      </div>
      <div className="rounded-lg border border-[var(--line)] bg-[var(--surface)]">
        <div className="flex items-center justify-between border-b border-[var(--line)] px-3 py-2 text-xs text-[var(--muted)]">
          <span>最近股票刷新状态</span>
          <span>{formatDate(state?.finishedAt || state?.startedAt) || "未运行"}</span>
        </div>
        {items.length === 0 ? (
          <p className="p-3 text-sm text-[var(--muted)]">暂无股票刷新状态。运行“最新行情刷新”后会更新这里。</p>
        ) : (
          <div className="divide-y divide-[var(--line-soft)]">
            {items.map((item) => (
              <div key={item.symbol} className="grid grid-cols-[80px_80px_minmax(0,1fr)_auto] items-center gap-3 px-3 py-2 text-xs">
                <span className="font-mono text-[var(--text)]">{item.symbol}</span>
                <Pill tone={quoteStatusTone(item.status)}>{quoteStatusLabel(item.status)}</Pill>
                <span className="truncate text-[var(--muted)]">
                  {item.errorMessage || `最近尝试 ${formatDate(item.lastAttemptAt) || "-"}`}
                </span>
                <span className="font-mono text-[var(--muted-strong)]">{formatDate(item.updatedAt) || "-"}</span>
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}

function MonitorTaskRow({
  task,
  submitting,
  onRun,
  onToggle,
  onEdit,
}: {
  task: StockV2MonitorTask;
  submitting: boolean;
  onRun: () => void;
  onToggle: () => void;
  onEdit: () => void;
}) {
  const def = task.definition;
  const cfg = task.config || {};
  const latest = task.latestRun;
  return (
    <div className="grid grid-cols-[minmax(0,1fr)_auto] items-start gap-3 rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3">
      <div className="min-w-0">
        <div className="flex flex-wrap items-center gap-2">
          <strong className="text-sm">{def.label || def.taskType}</strong>
          <Pill tone="neutral">{stockV2MonitorCategoryLabel(def.category)}</Pill>
          {cfg.enabled ? <Pill tone="good">已启用</Pill> : <Pill tone="neutral">未启用</Pill>}
          {cfg.agentDoublecheckEnabled ? <Pill tone="neutral">Agent 复核</Pill> : null}
        </div>
        <div className="mt-1.5 flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-[var(--muted-strong)]">
          <span>周期 {formatInterval(cfg.intervalSeconds)}</span>
          {cfg.cooldownSeconds ? <span>冷却 {formatInterval(cfg.cooldownSeconds)}</span> : null}
          {cfg.scope ? <span>范围 {cfg.scope}</span> : null}
          {cfg.sensitivity ? <span>敏感度 {stockV2MonitorSensitivityLabel(cfg.sensitivity)}</span> : null}
          <span>Agent {stockV2MonitorAgentStateLabel(cfg.agentDoublecheckEnabled ? "pending" : "not_enabled")}</span>
        </div>
        {def.description ? <p className="mt-1 text-xs text-[var(--muted)]">{def.description}</p> : null}
        <div className="mt-1 text-xs text-[var(--muted)]">
          最近运行:{latest ? `${stockV2MonitorRunStatusLabel(latest.status)} · 命中 ${latest.hitCount ?? 0} · ${formatDate(latest.startedAt) || "-"}` : "-"}
        </div>
        {latest && monitorSentinelPlanSummary(latest) ? (
          <div className="mt-1 text-xs text-[var(--muted)]">{monitorSentinelPlanSummary(latest)}</div>
        ) : null}
      </div>
      <div className="flex flex-wrap items-start justify-end gap-1">
        <Button onClick={onToggle} disabled={submitting} title={cfg.enabled ? "暂停" : "启用"}>
          <Power size={12} className="mr-1" />
          {cfg.enabled ? "暂停" : "启用"}
        </Button>
        <Button onClick={onRun} disabled={submitting} title="立即运行">
          <ArrowsClockwise size={12} className="mr-1" />
          运行
        </Button>
        <Button onClick={onEdit} disabled={submitting} title="配置">
          <Pencil size={12} className="mr-1" />
          配置
        </Button>
      </div>
    </div>
  );
}

function TaskConfigDrawer({
  task,
  submitting,
  onClose,
  onSubmit,
}: {
  task: StockV2MonitorTask;
  submitting: boolean;
  onClose: () => void;
  onSubmit: (input: StockV2MonitorTaskConfigInput) => Promise<void>;
}) {
  const def = task.definition;
  const cfg = task.config || {};
  const [enabled, setEnabled] = useState<boolean>(cfg.enabled || false);
  const [intervalSeconds, setIntervalSeconds] = useState(String(cfg.intervalSeconds ?? def.defaultConfig?.intervalSeconds ?? 600));
  const [cooldownSeconds, setCooldownSeconds] = useState(String(cfg.cooldownSeconds ?? def.defaultConfig?.cooldownSeconds ?? 0));
  const [scope, setScope] = useState(cfg.scope ?? def.defaultConfig?.scope ?? "");
  const [sensitivity, setSensitivity] = useState(cfg.sensitivity ?? def.defaultConfig?.sensitivity ?? "normal");
  const [agentDoublecheck, setAgentDoublecheck] = useState<boolean>(cfg.agentDoublecheckEnabled || false);

  function buildInput(): StockV2MonitorTaskConfigInput {
    return {
      enabled,
      intervalSeconds: Math.max(0, Number(intervalSeconds) || 0),
      cooldownSeconds: Math.max(0, Number(cooldownSeconds) || 0),
      scope: scope.trim() || undefined,
      sensitivity,
      agentDoublecheckEnabled: agentDoublecheck,
    };
  }

  return (
    <Drawer title={`配置 · ${def.label || def.taskType}`} subtitle={def.description} onClose={onClose} width={460}>
      <div className="grid gap-3">
        <label className="flex min-h-10 items-center justify-between gap-3 rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] px-3 text-sm">
          <span>启用周期执行</span>
          <input type="checkbox" checked={enabled} onChange={(e) => setEnabled(e.target.checked)} className="h-4 w-4 accent-[var(--accent)]" />
        </label>
        <Field label="执行周期(秒)" help="后台调度按此周期检查任务是否到点执行。">
          <input type="number" min="0" step="1" value={intervalSeconds} onChange={(e) => setIntervalSeconds(e.target.value)} />
        </Field>
        <Field label="冷却时间(秒)" help="同一规则命中后的最短重复提醒间隔。">
          <input type="number" min="0" step="1" value={cooldownSeconds} onChange={(e) => setCooldownSeconds(e.target.value)} />
        </Field>
        <Field label="作用范围" help="留空表示使用系统默认范围。可按后端约定填写 all、hot、portfolio、strategy 等范围。">
          <input type="text" value={scope} placeholder="默认范围" onChange={(e) => setScope(e.target.value)} />
        </Field>
        <Field label="敏感度">
          <select value={sensitivity} onChange={(e) => setSensitivity(e.target.value)}>
            <option value="low">低</option>
            <option value="normal">标准</option>
            <option value="high">高</option>
          </select>
        </Field>
        <label className="flex min-h-10 items-center justify-between gap-3 rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] px-3 text-sm">
          <span>Agent doublecheck(预留)</span>
          <input type="checkbox" checked={agentDoublecheck} onChange={(e) => setAgentDoublecheck(e.target.checked)} className="h-4 w-4 accent-[var(--accent)]" />
        </label>
        <p className="text-xs leading-relaxed text-[var(--muted)]">
          监控只产生命中候选与提醒,不直接生成买卖建议,不会修改持仓。
        </p>
        <div className="flex justify-end gap-2 border-t border-[var(--line)] pt-3">
          <Button onClick={onClose}>取消</Button>
          <Button tone="primary" disabled={submitting} onClick={() => void onSubmit(buildInput())}>
            {submitting ? "保存中…" : "保存配置"}
          </Button>
        </div>
      </div>
    </Drawer>
  );
}

function MonitorRunRow({
  run,
  hits,
  agentDetails,
  onOpen,
}: {
  run: StockV2MonitorRun;
  hits: StockV2MonitorHit[];
  agentDetails: StockV2AgentExecutionDetail[];
  onOpen: () => void;
}) {
  const symbols = affectedSymbols(hits);
  const statusTone = stockV2MonitorRunStatusTone(run.status);
  const hitCount = run.hitCount ?? hits.length;
  return (
    <button
      className={`w-full rounded-lg border bg-[var(--surface)] text-left text-xs transition hover:bg-[var(--surface-soft)] ${run.status === "running" ? "border-[rgba(199,85,8,0.28)]" : "border-[var(--line)]"}`}
      onClick={onOpen}
      type="button"
    >
      <div className="p-3">
        <div className="flex flex-wrap items-center gap-2">
          <strong className="text-sm">{stockV2MonitorTaskTypeLabel(run.taskType)}</strong>
          <Pill tone={statusTone}>{stockV2MonitorRunStatusLabel(run.status)}</Pill>
          <Pill tone={hitCount > 0 ? "warn" : "neutral"}>{hitCount > 0 ? `命中候选 ${hitCount}` : "未命中候选"}</Pill>
          <Pill tone={monitorAgentTone(run, hits, agentDetails)}>Agent {monitorAgentSummary(run, hits, agentDetails)}</Pill>
          {run.triggerType ? <span className="text-[var(--muted)]">· {monitorTriggerLabel(run.triggerType)}</span> : null}
        </div>
        <div className="mt-2 flex flex-wrap items-center gap-x-4 gap-y-1 text-[var(--muted-strong)]">
          <span>扫描 {run.scannedCount ?? 0}</span>
          <span>成功 {run.successCount ?? 0}</span>
          <span className={(run.failedCount ?? 0) > 0 ? "text-[var(--danger)]" : ""}>失败 {run.failedCount ?? 0}</span>
          <span>影响股票 {symbols.length ? compactList(symbols) : "-"}</span>
          <span>{affectedObjectSummary(hits)}</span>
        </div>
        {run.errorMessage ? <p className="mt-1 break-words text-[var(--danger)]">{run.errorMessage}</p> : null}
        <div className="mt-2 flex flex-wrap items-center gap-x-4 gap-y-1 text-[var(--muted)]">
          <span>{formatDate(run.startedAt) || "-"} → {formatDate(run.finishedAt) || (run.status === "running" ? "进行中" : "-")}</span>
          {run.scopeSummary ? <span>{run.scopeSummary}</span> : null}
          <span className="text-[var(--accent)]">点击查看详情</span>
        </div>
      </div>
    </button>
  );
}

function MonitorRunDrawer({
  run,
  hits,
  agentDetails,
  agentLoading,
  loading,
  onClose,
  onOpenAgentRun,
  onOpenReview,
}: {
  run: StockV2MonitorRun;
  hits: StockV2MonitorHit[];
  agentDetails: StockV2AgentExecutionDetail[];
  agentLoading: boolean;
  loading: boolean;
  onClose: () => void;
  onOpenAgentRun: (runId: string) => void;
  onOpenReview: (hitId: string) => void;
}) {
  const symbols = affectedSymbols(hits);
  const portfolioSentinel = run.taskType === "portfolio_sentinel";
  return (
    <Drawer
      title={portfolioSentinel ? "组合哨兵派生详情" : `监控任务详情 · ${stockV2MonitorTaskTypeLabel(run.taskType)}`}
      subtitle={
        portfolioSentinel
          ? `${stockV2MonitorRunStatusLabel(run.status)} · 从组合哨兵结果派生 MonitorHit / Review / Alert · ${formatDate(run.startedAt) || "-"}`
          : `${stockV2MonitorRunStatusLabel(run.status)} · ${monitorTriggerLabel(run.triggerType)} · ${formatDate(run.startedAt) || "-"}`
      }
      onClose={onClose}
      width={620}
    >
      <div className="grid gap-4 text-sm">
        {portfolioSentinel ? (
          <Notice>
            <span className="text-xs">
              这不是组合哨兵的主运行记录,而是哨兵结论进入通用监控流水线后的派生记录。完整窗口上下文和 Agent 报告在组合哨兵页查看。
            </span>
          </Notice>
        ) : null}

        <div className="grid grid-cols-2 gap-2">
          <SummaryCell label="扫描对象" value={String(run.scannedCount ?? 0)} tone="neutral" />
          <SummaryCell label="命中候选" value={String(run.hitCount ?? hits.length)} tone={(run.hitCount ?? hits.length) > 0 ? "warn" : "neutral"} />
          <SummaryCell label="影响股票" value={symbols.length ? compactList(symbols, 2) : "-"} tone={symbols.length ? "warn" : "neutral"} />
          <SummaryCell label="Agent 复核" value={monitorAgentSummary(run, hits, agentDetails)} tone={monitorAgentTone(run, hits, agentDetails)} />
        </div>

        <div className="rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3 text-xs">
          <div className="grid gap-1.5">
            <div className="flex justify-between gap-3"><span className="text-[var(--muted)]">任务类型</span><span>{stockV2MonitorTaskTypeLabel(run.taskType)}</span></div>
            <div className="flex justify-between gap-3"><span className="text-[var(--muted)]">执行时间</span><span>{formatDate(run.startedAt) || "-"} → {formatDate(run.finishedAt) || (run.status === "running" ? "进行中" : "-")}</span></div>
            <div className="flex justify-between gap-3"><span className="text-[var(--muted)]">结果计数</span><span>成功 {run.successCount ?? 0} / 失败 {run.failedCount ?? 0} / Alert {run.alertCount ?? 0}</span></div>
            {monitorSentinelPlanSummary(run) ? (
              <div className="flex justify-between gap-3"><span className="text-[var(--muted)]">哨兵操作计划</span><span>{monitorSentinelPlanSummary(run)}</span></div>
            ) : null}
            {run.scopeSummary ? <div className="flex justify-between gap-3"><span className="text-[var(--muted)]">扫描范围</span><span>{run.scopeSummary}</span></div> : null}
            {portfolioSentinelRunID(run) ? <div className="flex justify-between gap-3"><span className="text-[var(--muted)]">哨兵运行</span><span className="font-mono">{shortID(portfolioSentinelRunID(run))}</span></div> : null}
            {run.errorMessage ? <div className="break-words text-[var(--danger)]">错误：{run.errorMessage}</div> : null}
          </div>
        </div>

        {portfolioSentinel ? (
          <div className="flex justify-end">
            <Button onClick={openPortfolioSentinelPage} title="查看组合哨兵">
              <ShieldCheck size={12} className="mr-1" />
              查看哨兵
            </Button>
          </div>
        ) : null}

        <div>
          <div className="mb-2 flex items-center justify-between">
            <strong className="text-sm">Agent 执行</strong>
            <span className="text-xs text-[var(--muted)]">{agentLoading ? "加载中…" : `${agentDetails.length} 次`}</span>
          </div>
          {agentLoading ? (
            <p className="text-xs text-[var(--muted)]">加载 Agent 执行详情…</p>
          ) : (
            <StockV2AgentExecutionSummaryList items={agentDetails} onOpen={onOpenAgentRun} />
          )}
        </div>

        <div>
          <div className="mb-2 flex items-center justify-between">
            <strong className="text-sm">命中候选</strong>
            <span className="text-xs text-[var(--muted)]">{loading ? "加载中…" : `${hits.length} 条`}</span>
          </div>
          {loading ? (
            <p className="text-xs text-[var(--muted)]">加载命中详情…</p>
          ) : hits.length === 0 ? (
            <div className="rounded-lg border border-dashed border-[var(--line)] bg-[var(--surface-soft)] p-4 text-center text-xs text-[var(--muted)]">
              本次监控没有产生命中候选。
            </div>
          ) : (
            <div className="grid gap-2">
              {hits.map((hit) => (
                <MonitorHitDetail
                  key={hit.id}
                  hit={hit}
                  onOpenAgentRun={onOpenAgentRun}
                  onOpenReview={onOpenReview}
                />
              ))}
            </div>
          )}
        </div>

        {run.metadata && Object.keys(run.metadata).length > 0 ? (
          <div>
            <strong className="text-sm">运行上下文</strong>
            <pre className="mt-2 max-h-48 overflow-auto whitespace-pre-wrap break-words rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3 text-xs text-[var(--muted-strong)]">
              {stringifyJSON(run.metadata)}
            </pre>
          </div>
        ) : null}
      </div>
    </Drawer>
  );
}

function openPortfolioSentinelPage() {
  const href = buildQueryHref({ tab: "stockv2", stockv2: "sentinel" });
  const current = `${window.location.pathname}${window.location.search}${window.location.hash}`;
  if (href !== current) {
    window.history.pushState(null, "", href);
    window.dispatchEvent(new PopStateEvent("popstate"));
  }
}

function portfolioSentinelRunID(run: StockV2MonitorRun): string {
  const value = run.metadata?.portfolioSentinelRunId;
  return typeof value === "string" ? value : "";
}

function monitorSentinelPlanSummary(run: StockV2MonitorRun): string {
  const metadata = run.metadata;
  const total = metadataNumber(metadata?.portfolioSentinelPlanCount);
  if (total <= 0) return "";
  const evaluated = metadataNumber(metadata?.portfolioSentinelPlanEvaluatedCount);
  const matched = metadataNumber(metadata?.portfolioSentinelPlanMatchedCount);
  const triggered = metadataNumber(metadata?.portfolioSentinelPlanTriggeredCount);
  const expired = metadataNumber(metadata?.portfolioSentinelPlanExpiredCount);
  const pending = metadataNumber(metadata?.portfolioSentinelPlanPendingCount);
  return `共 ${total} 条，检查 ${evaluated}，命中 ${matched}，已触发 ${triggered}，待生效 ${pending}，已过期 ${expired}`;
}

function metadataNumber(value: unknown): number {
  return typeof value === "number" && Number.isFinite(value) ? value : 0;
}

function MonitorHitDetail({
  hit,
  onOpenAgentRun,
  onOpenReview,
}: {
  hit: StockV2MonitorHit;
  onOpenAgentRun: (runId: string) => void;
  onOpenReview: (hitId: string) => void;
}) {
  const evidence = hit.evidence || {};
  const hasEvidence = Object.keys(evidence).length > 0;
  const matchedActionLabel = evidenceStr(evidence, "matchedActionLabel") || evidenceStr(evidence, "matchedAction");
  const prefilterKey = evidenceStr(evidence, "matchedPrefilterKey");
  const prefilterType = evidenceStr(evidence, "matchedPrefilterType");
  const playbookRule = evidenceStr(evidence, "playbookRule");
  const pipeline = reviewPipelineFromHit(hit);
  const reviewId = pipeline.reviewId || "";
  const agentRunId = pipeline.agentRunId || (hit.agentDecisionId && hit.agentDecisionId !== "pending" ? hit.agentDecisionId : "");
  const hasReview = Boolean(reviewId) || hit.status === "reviewed" || hit.status === "ignored";
  return (
    <div className="rounded-lg border border-[var(--line)] bg-[var(--surface)] p-3 text-xs">
      <div className="flex flex-wrap items-center gap-2">
        <Pill tone={stockV2MonitorHitStatusTone(hit.status)}>{stockV2MonitorHitStatusLabel(hit.status)}</Pill>
        {hit.symbol ? <Pill tone="neutral">{hit.symbol}</Pill> : null}
        <strong className="text-sm">{hit.title || "(无标题)"}</strong>
      </div>
      {hit.summary ? <p className="mt-1 break-words leading-relaxed text-[var(--muted-strong)]">{hit.summary}</p> : null}
      <div className="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-[var(--muted)]">
        {hit.strategyId ? <span>策略 {hit.strategyId.slice(0, 8)}</span> : null}
        {hit.portfolioId ? <span>组合 {hit.portfolioId.slice(0, 8)}</span> : null}
        <span>Agent {hitAgentLabel(hit)}</span>
        <span>{formatDate(hit.createdAt) || "-"}</span>
      </div>

      <MonitorHitReviewPipeline pipeline={pipeline} agentRunId={agentRunId} reviewId={reviewId} />

      {(matchedActionLabel || prefilterKey || playbookRule) && hasEvidence ? (
        <div className="mt-2 grid grid-cols-[80px_minmax(0,1fr)] gap-x-3 gap-y-1 rounded border border-[var(--line)] bg-[var(--surface-soft)] px-2 py-2 text-[var(--muted-strong)]">
          {matchedActionLabel ? (
            <>
              <span className="text-[var(--muted)]">命中动作</span>
              <span className="break-words">{matchedActionLabel}</span>
            </>
          ) : null}
          {prefilterKey || prefilterType ? (
            <>
              <span className="text-[var(--muted)]">预过滤</span>
              <span className="break-words">{[prefilterType, prefilterKey].filter(Boolean).join(" · ")}</span>
            </>
          ) : null}
          {playbookRule ? (
            <>
              <span className="text-[var(--muted)]">规则</span>
              <span className="break-words">{playbookRule}</span>
            </>
          ) : null}
        </div>
      ) : null}

      {hasEvidence ? (
        <details className="mt-2 rounded border border-[var(--line)] bg-[var(--surface-soft)]">
          <summary className="cursor-pointer px-2 py-1 text-[var(--muted)]">原始 evidence</summary>
          <pre className="max-h-40 overflow-auto whitespace-pre-wrap break-words px-2 py-2 text-[11px] text-[var(--muted-strong)]">
            {stringifyJSON(hit.evidence)}
          </pre>
        </details>
      ) : null}

      <div className="mt-2 flex flex-wrap justify-end gap-1.5">
        {agentRunId ? (
          <Button onClick={() => onOpenAgentRun(agentRunId)} title="查看 Agent 执行详情">
            查看 Agent 执行详情
          </Button>
        ) : null}
        <Button
          tone={hasReview ? "neutral" : "primary"}
          onClick={() => onOpenReview(hit.id)}
          title={hasReview ? "查看 Review" : "进入 Review"}
        >
          <MagnifyingGlass size={12} className="mr-1" />
          {hasReview ? "查看 Review" : "进入 Review"}
        </Button>
      </div>
    </div>
  );
}

function MonitorHitReviewPipeline({
  pipeline,
  agentRunId,
  reviewId,
}: {
  pipeline: StockV2MonitorReviewPipeline;
  agentRunId: string;
  reviewId: string;
}) {
  if (!reviewId && !pipeline.agentStatus && !agentRunId && !pipeline.error && !pipeline.agentError) return null;
  return (
    <div className="mt-2 flex flex-wrap items-center gap-1.5 rounded border border-[var(--line)] bg-[var(--surface-soft)] px-2 py-2">
      <span className="text-[var(--muted)]">Review</span>
      {reviewId ? (
        <Pill tone={stockV2ReviewToneFromStatus(pipeline.reviewStatus)}>{stockV2ReviewLabelFromStatus(pipeline.reviewStatus)}</Pill>
      ) : (
        <Pill tone="warn">未创建</Pill>
      )}
      {pipeline.reviewCreated === true ? <Pill tone="neutral">本次创建</Pill> : null}
      <span className="ml-1 text-[var(--muted)]">Agent</span>
      <Pill tone={agentPipelineTone(pipeline, agentRunId)}>{agentPipelineLabel(pipeline, agentRunId)}</Pill>
      {agentRunId ? <span className="font-mono text-[11px] text-[var(--muted-strong)]">{agentRunId.slice(0, 12)}</span> : null}
      {pipeline.agentSkippedReason ? (
        <span className="min-w-0 break-words text-[var(--muted)]">{pipeline.agentSkippedReason}</span>
      ) : null}
      {pipeline.agentError || pipeline.error ? (
        <span className="min-w-0 break-words text-[var(--danger)]">{pipeline.agentError || pipeline.error}</span>
      ) : null}
    </div>
  );
}

function reviewPipelineFromHit(hit: StockV2MonitorHit): StockV2MonitorReviewPipeline {
  const pipeline = mapFromAny(hit.evidence?.reviewPipeline);
  return {
    reviewId: readPipelineString(pipeline, "reviewId"),
    reviewCreated: pipeline.reviewCreated === true,
    reviewStatus: readPipelineString(pipeline, "reviewStatus"),
    agentDoublecheckEnabled: pipeline.agentDoublecheckEnabled === true,
    agentAttempted: pipeline.agentAttempted === true,
    agentStatus: readPipelineString(pipeline, "agentStatus"),
    agentSkippedReason: readPipelineString(pipeline, "agentSkippedReason"),
    agentRunId: readPipelineString(pipeline, "agentRunId"),
    agentRunStatus: readPipelineString(pipeline, "agentRunStatus"),
    agentError: readPipelineString(pipeline, "agentError"),
    error: readPipelineString(pipeline, "error"),
  };
}

function stockV2ReviewLabelFromStatus(status?: string): string {
  return status ? stockV2ReviewStatusLabel(status) : "已创建";
}

function stockV2ReviewToneFromStatus(status?: string): "good" | "warn" | "danger" | "neutral" {
  return status ? stockV2ReviewStatusTone(status) : "neutral";
}

function agentPipelineLabel(pipeline: StockV2MonitorReviewPipeline, agentRunId: string): string {
  if (pipeline.agentRunStatus) return stockV2AgentRunStatusLabel(pipeline.agentRunStatus);
  switch (pipeline.agentStatus) {
    case "started": return "已触发";
    case "enabled_no_executor": return "就绪(无执行器)";
    case "unavailable": return "不可用";
    case "skipped": return "已跳过";
    case "not_enabled": return "未启用";
    default: return agentRunId ? "已关联" : stockV2MonitorAgentStateLabel(pipeline.agentStatus);
  }
}

function agentPipelineTone(
  pipeline: StockV2MonitorReviewPipeline,
  agentRunId: string,
): "good" | "warn" | "danger" | "neutral" {
  if (pipeline.agentRunStatus) return stockV2AgentRunStatusTone(pipeline.agentRunStatus);
  if (pipeline.agentStatus === "unavailable" || pipeline.agentError) return "danger";
  if (pipeline.agentStatus === "enabled_no_executor") return "warn";
  if (pipeline.agentStatus === "started" || agentRunId) return "good";
  return "neutral";
}

function mapFromAny(value: unknown): Record<string, unknown> {
  return value && typeof value === "object" && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {};
}

function readPipelineString(map: Record<string, unknown>, key: string): string {
  const value = map[key];
  if (value === undefined || value === null || value === "") return "";
  return typeof value === "object" ? JSON.stringify(value) : String(value);
}

function evidenceStr(evidence: Record<string, unknown>, key: string): string {
  const v = evidence[key];
  if (v === undefined || v === null || v === "") return "";
  return typeof v === "object" ? JSON.stringify(v) : String(v);
}

function AlertRow({
  alert,
  submitting,
  onOpen,
  onAck,
  onIgnore,
  onResolve,
}: {
  alert: StockV2Alert;
  submitting: boolean;
  onOpen: () => void;
  onAck: () => void;
  onIgnore: () => void;
  onResolve: () => void;
}) {
  const open = alert.status === "open";
  const evidence = alert.evidence || {};
  const degradedReason = evidenceStr(evidence, "degraded_reason");
  const triggerDecision = evidenceStr(evidence, "trigger_decision");
  const agentSummary = evidenceStr(evidence, "agent_summary") || evidenceStr(evidence, "agentRunStatus");
  const reviewLabel = alert.reviewStatus ? stockV2ReviewStatusLabel(alert.reviewStatus) : (alert.reviewId ? "已创建" : "-");
  return (
    <div className="grid grid-cols-[minmax(0,1fr)_auto] items-start gap-3 rounded-lg border border-[var(--line)] bg-[var(--surface)] p-3">
      <button className="min-w-0 text-left" type="button" onClick={onOpen}>
        <div className="flex flex-wrap items-center gap-2">
          <Pill tone={stockV2AlertStatusTone(alert.status)}>{stockV2AlertStatusLabel(alert.status)}</Pill>
          <Pill tone={stockV2AlertLevelTone(alert.level)}>{stockV2AlertLevelLabel(alert.level)}</Pill>
          {alert.taskType ? <Pill tone="neutral">{stockV2MonitorTaskTypeLabel(alert.taskType)}</Pill> : null}
          {alert.triggerSource ? <Pill tone={alertTriggerSourceTone(alert.triggerSource)}>{alertTriggerSourceLabel(alert.triggerSource)}</Pill> : null}
          {alert.symbol ? <Pill tone="neutral">{alert.symbol}</Pill> : null}
          <strong className="truncate text-sm">{alert.title || "(无标题)"}</strong>
        </div>
        {alert.summary ? <p className="mt-1 break-words text-xs leading-relaxed text-[var(--muted-strong)]">{alert.summary}</p> : null}
        <div className="mt-2 flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-[var(--muted-strong)]">
          {alert.strategyId ? <span>策略 {shortID(alert.strategyId)}</span> : null}
          {alert.portfolioId ? <span>组合 {shortID(alert.portfolioId)}</span> : null}
          <span>Review {reviewLabel}</span>
          <span>次数 {alert.occurrenceCount ?? 1}</span>
          <span>最近 {formatDate(alert.lastSeenAt || alert.triggeredAt || alert.createdAt) || "-"}</span>
        </div>
        {(degradedReason || agentSummary || triggerDecision) ? (
          <div className="mt-1 truncate text-xs text-[var(--muted)]">
            {degradedReason ? `降级原因 ${degradedReason}` : agentSummary ? `Agent ${agentSummary}` : `触发判断 ${triggerDecision}`}
          </div>
        ) : null}
        <div className="mt-1 text-xs text-[var(--accent)]">点击查看证据与关联对象</div>
      </button>
      <div className="flex flex-wrap items-start justify-end gap-1">
        {open ? (
          <Button onClick={onAck} disabled={submitting} title="确认">
            <Check size={12} className="mr-1" />
            确认
          </Button>
        ) : null}
        {open || alert.status === "acknowledged" ? (
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

function AlertDrawer({
  alert,
  onClose,
  onOpenAgentRun,
  onOpenReview,
}: {
  alert: StockV2Alert;
  onClose: () => void;
  onOpenAgentRun: (runId: string) => void;
  onOpenReview: (hitId: string) => void;
}) {
  const evidence = alert.evidence || {};
  const degradedReason = evidenceStr(evidence, "degraded_reason");
  const triggerDecision = evidenceStr(evidence, "trigger_decision");
  return (
    <Drawer
      title={`提醒详情 · ${alert.title || alert.id}`}
      subtitle={`${stockV2AlertStatusLabel(alert.status)} · ${alertTriggerSourceLabel(alert.triggerSource)} · ${formatDate(alert.lastSeenAt || alert.triggeredAt || alert.createdAt) || "-"}`}
      onClose={onClose}
      width={560}
    >
      <div className="grid gap-4 text-sm">
        <div className="grid grid-cols-2 gap-2">
          <SummaryCell label="来源任务" value={alert.taskType ? stockV2MonitorTaskTypeLabel(alert.taskType) : "-"} tone="neutral" />
          <SummaryCell label="触发来源" value={alertTriggerSourceLabel(alert.triggerSource)} tone={alertTriggerSourceTone(alert.triggerSource)} />
          <SummaryCell label="发生次数" value={String(alert.occurrenceCount ?? 1)} tone={(alert.occurrenceCount ?? 1) > 1 ? "warn" : "neutral"} />
          <SummaryCell label="Review" value={alert.reviewStatus ? stockV2ReviewStatusLabel(alert.reviewStatus) : (alert.reviewId ? "已创建" : "-")} tone={stockV2ReviewToneFromStatus(alert.reviewStatus)} />
        </div>

        <div className="rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3 text-xs">
          <div className="grid gap-1.5">
            {alert.symbol ? <KeyValue label="股票" value={[alert.market, alert.symbol].filter(Boolean).join(" ")} /> : null}
            {alert.strategyId ? <KeyValue label="策略" value={alert.strategyId} mono /> : null}
            {alert.portfolioId ? <KeyValue label="组合" value={alert.portfolioId} mono /> : null}
            {alert.monitorRunId ? <KeyValue label="MonitorRun" value={alert.monitorRunId} mono /> : null}
            {alert.monitorHitId ? <KeyValue label="MonitorHit" value={alert.monitorHitId} mono /> : null}
            {alert.reviewId ? <KeyValue label="ReviewID" value={alert.reviewId} mono /> : null}
            {alert.agentRunId ? <KeyValue label="AgentRun" value={alert.agentRunId} mono /> : null}
            {alert.decisionLedgerId ? <KeyValue label="DecisionLedger" value={alert.decisionLedgerId} mono /> : null}
            {alert.dedupeKey ? <KeyValue label="Dedupe" value={alert.dedupeKey} mono /> : null}
          </div>
        </div>

        {alert.summary ? (
          <div>
            <strong className="text-sm">摘要</strong>
            <p className="mt-1 text-xs leading-relaxed text-[var(--muted-strong)]">{alert.summary}</p>
          </div>
        ) : null}

        <div className="rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3 text-xs">
          <div className="grid gap-1.5">
            <KeyValue label="触发判断" value={triggerDecision || "-"} />
            {degradedReason ? <KeyValue label="降级原因" value={degradedReason} /> : null}
            <KeyValue label="首次出现" value={formatDate(alert.firstSeenAt || alert.createdAt) || "-"} />
            <KeyValue label="最近出现" value={formatDate(alert.lastSeenAt || alert.triggeredAt || alert.createdAt) || "-"} />
          </div>
        </div>

        <div>
          <strong className="text-sm">Evidence</strong>
          <pre className="mt-2 max-h-72 overflow-auto whitespace-pre-wrap break-words rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3 text-xs text-[var(--muted-strong)]">
            {stringifyJSON(evidence)}
          </pre>
        </div>

        <div className="flex flex-wrap justify-end gap-2 border-t border-[var(--line)] pt-3">
          {alert.agentRunId ? (
            <Button onClick={() => onOpenAgentRun(alert.agentRunId || "")}>查看 Agent 执行</Button>
          ) : null}
          {alert.monitorHitId ? (
            <Button tone="primary" onClick={() => onOpenReview(alert.monitorHitId || "")}>
              查看 Review
            </Button>
          ) : null}
        </div>
      </div>
    </Drawer>
  );
}

function KeyValue({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="grid grid-cols-[96px_minmax(0,1fr)] gap-3">
      <span className="text-[var(--muted)]">{label}</span>
      <span className={`break-words ${mono ? "font-mono" : ""}`}>{value}</span>
    </div>
  );
}

function alertTriggerSourceLabel(source?: string): string {
  switch (source) {
    case "agent_confirmed": return "Agent 确认";
    case "manual_review_confirmed": return "人工确认";
    case "deterministic": return "确定性触发";
    case "degraded": return "降级触发";
    default: return source || "-";
  }
}

function alertTriggerSourceTone(source?: string): "good" | "warn" | "danger" | "neutral" {
  switch (source) {
    case "agent_confirmed": return "good";
    case "manual_review_confirmed": return "good";
    case "deterministic": return "neutral";
    case "degraded": return "warn";
    default: return "neutral";
  }
}

function shortID(value?: string): string {
  if (!value) return "-";
  return value.length > 8 ? value.slice(0, 8) : value;
}

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

function quoteRefreshSubtitle(state?: StockV2QuoteRefreshTaskState): string {
  if (!state || !state.startedAt) {
    return "高频行情刷新使用独立状态表,不写入监控任务历史";
  }
  return `${quoteRefreshTaskStatusLabel(state.status)} · 扫描 ${state.scannedCount ?? 0} · 成功 ${state.successCount ?? 0} · 失败 ${state.failedCount ?? 0}`;
}

function quoteRefreshTaskStatusLabel(status?: string): string {
  if (status === "idle") return "未运行";
  return stockV2MonitorRunStatusLabel(status);
}

function quoteRefreshTaskStatusTone(status?: string): "good" | "warn" | "danger" | "neutral" {
  if (status === "idle") return "neutral";
  return stockV2MonitorRunStatusTone(status);
}

function quoteStatusLabel(status?: string): string {
  switch (status) {
    case "fresh": return "成功";
    case "failed": return "失败";
    case "stale": return "旧价";
    case "estimated": return "估算";
    default: return status || "-";
  }
}

function quoteStatusTone(status?: string): "good" | "warn" | "danger" | "neutral" {
  switch (status) {
    case "fresh": return "good";
    case "failed": return "danger";
    case "stale":
    case "estimated": return "warn";
    default: return "neutral";
  }
}

function formatInterval(seconds?: number): string {
  if (!seconds || seconds <= 0) return "手动";
  if (seconds < 60) return `${seconds}s`;
  if (seconds < 3600) return `${Math.round(seconds / 60)}min`;
  const hours = Math.round(seconds / 3600);
  if (hours < 24) return `${hours}h`;
  return `${Math.round(hours / 24)}d`;
}

function stockV2MonitorSensitivityLabel(value?: string): string {
  switch (value) {
    case "low": return "低";
    case "high": return "高";
    case "normal": return "标准";
    default: return value || "标准";
  }
}

function affectedSymbols(hits: StockV2MonitorHit[]): string[] {
  return uniqueStrings(hits.map((hit) => hit.symbol || ""));
}

function affectedObjectSummary(hits: StockV2MonitorHit[]): string {
  const strategyCount = uniqueStrings(hits.map((hit) => hit.strategyId || "")).length;
  const portfolioCount = uniqueStrings(hits.map((hit) => hit.portfolioId || "")).length;
  const parts = [];
  if (strategyCount > 0) parts.push(`策略 ${strategyCount}`);
  if (portfolioCount > 0) parts.push(`组合 ${portfolioCount}`);
  return parts.length ? parts.join(" / ") : "无关联策略/组合";
}

function compactList(items: string[], max = 3): string {
  if (items.length <= max) return items.join("、");
  return `${items.slice(0, max).join("、")} 等 ${items.length} 个`;
}

function monitorTriggerLabel(triggerType?: string): string {
  switch (triggerType) {
    case "manual": return "手动触发";
    case "scheduled": return "定时触发";
    case "event": return "事件触发";
    default: return triggerType || "-";
  }
}

function monitorAgentSummary(run: StockV2MonitorRun, hits: StockV2MonitorHit[], agentDetails: StockV2AgentExecutionDetail[] = []): string {
  if (agentDetails.length > 0) {
    const running = agentDetails.filter((detail) => detail.run.status === "running").length;
    const failed = agentDetails.filter((detail) => detail.run.status === "failed").length;
    const completed = agentDetails.filter((detail) => detail.run.status === "completed").length;
    if (running > 0) return `运行中 ${running}`;
    if (failed > 0 && completed === 0) return `失败 ${failed}`;
    return `已触发 ${agentDetails.length}`;
  }
  const state = String(run.metadata?.agentDoublecheck || "");
  if (state === "not_enabled" || !state) return "未启用";
  const pipelines = hits.map(reviewPipelineFromHit);
  if (hits.some((hit) => hit.agentDecisionId && hit.agentDecisionId !== "pending") || pipelines.some((pipeline) => pipeline.agentRunId)) return "已触发";
  if (pipelines.some((pipeline) => pipeline.agentStatus === "unavailable" || pipeline.agentError)) return "不可用";
  if (pipelines.some((pipeline) => pipeline.agentStatus === "enabled_no_executor")) return "已启用 · 未接执行器";
  if (pipelines.some((pipeline) => pipeline.reviewId)) return "Review 已创建";
  if ((run.hitCount ?? hits.length) <= 0) return "已启用 · 无候选";
  if (state === "enabled_no_executor") return "已启用 · 未接执行器";
  return stockV2MonitorAgentStateLabel(state);
}

function monitorAgentTone(run: StockV2MonitorRun, hits: StockV2MonitorHit[], agentDetails: StockV2AgentExecutionDetail[] = []): "good" | "warn" | "danger" | "neutral" {
  if (agentDetails.some((detail) => detail.run.status === "failed")) return "danger";
  if (agentDetails.some((detail) => detail.run.status === "running")) return "warn";
  if (agentDetails.length > 0) return "good";
  const summary = monitorAgentSummary(run, hits, agentDetails);
  if (summary.includes("已触发") || summary.includes("Review 已创建")) return "good";
  if (summary.includes("不可用")) return "danger";
  if (summary.includes("未接") || summary.includes("待")) return "warn";
  return "neutral";
}

function hitAgentLabel(hit: StockV2MonitorHit): string {
  if (hit.agentDecisionId && hit.agentDecisionId !== "pending") return `结果 ${hit.agentDecisionId.slice(0, 8)}`;
  const pipeline = reviewPipelineFromHit(hit);
  if (pipeline.agentRunId) return `Run ${pipeline.agentRunId.slice(0, 8)}`;
  if (pipeline.agentStatus) return agentPipelineLabel(pipeline, "");
  const state = String(hit.evidence?.agentDoublecheck || "");
  if (state === "enabled_no_executor") return "已启用 · 未接执行器";
  if (state === "unavailable") return "不可用";
  if (state === "started") return "已触发";
  if (state === "skipped") return "已跳过";
  if (hit.agentDecisionId === "pending" || state === "pending") return "待结果";
  return "未启用";
}

function stringifyJSON(value: unknown): string {
  try {
    return JSON.stringify(value, null, 2);
  } catch {
    return String(value);
  }
}

function uniqueStrings(values: string[]): string[] {
  return Array.from(new Set(values.map((value) => value.trim()).filter(Boolean)));
}

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
