import { useEffect, useRef, useState } from "react";
import { ArrowSquareOut, MagnifyingGlass } from "@phosphor-icons/react";
import type { AppActions } from "../../app/App";
import type {
  ApiError,
  StockV2AgentListResponse,
  StockV2OpportunityDiscoveryRun,
  StockV2OpportunityDiscoveryStep,
} from "../../app/types";
import { friendlyError } from "../../api/client";
import { Button, Drawer, Notice, Pill } from "../../components/ui";
import {
  stockV2DiscoveryRunStatusLabel,
  stockV2DiscoveryRunStatusTone,
  stockV2DiscoveryStepLabel,
  stockV2DiscoveryStepStatusLabel,
} from "../../domain/labels";
import { StockV2AgentRunDetailDrawer } from "./StockV2AgentExecutionLedger";
import { StockV2OpportunityStepTimeline } from "./StockV2OpportunityStepTimeline";

const POLL_INTERVAL_MS = 2500;

// 机会发现运行 Drawer（核心可观测性）：顶部状态条 + 左 Timeline + 右 Step Detail。
// 运行中（pending/running）链式 setTimeout 轮询 run + steps；终态或 404 停止。
export function StockV2OpportunityRunDrawer({
  actions,
  runId,
  onClose,
}: {
  actions: AppActions;
  runId: string;
  onClose: () => void;
}) {
  const [run, setRun] = useState<StockV2OpportunityDiscoveryRun | null>(null);
  const [steps, setSteps] = useState<StockV2OpportunityDiscoveryStep[]>([]);
  const [selectedStepId, setSelectedStepId] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [agentRunDrawerId, setAgentRunDrawerId] = useState<string | null>(null);
  const prevStatusRef = useRef<string | null>(null);
  // api 是模块级函数、setToast 是 useCallback，均为稳定引用；不要依赖整个 actions 对象，
  // 避免 actions 因 csrf 刷新变引用时重置轮询、清空 prevStatusRef 导致完成 toast 漏发。
  const { api, setToast } = actions;

  useEffect(() => {
    let mounted = true;
    let timer: number | undefined;
    prevStatusRef.current = null;

    async function poll() {
      try {
        const [r, s] = await Promise.all([
          api<StockV2OpportunityDiscoveryRun>(
            `/api/stockv2/opportunity-discovery-runs/${encodeURIComponent(runId)}`,
          ),
          api<StockV2AgentListResponse<StockV2OpportunityDiscoveryStep>>(
            `/api/stockv2/opportunity-discovery-runs/${encodeURIComponent(runId)}/steps`,
          ),
        ]);
        if (!mounted) return;
        setRun(r);
        setSteps(s.items || []);
        setError(null);
        setLoading(false);

        const prev = prevStatusRef.current;
        prevStatusRef.current = r.status;
        const active = r.status === "pending" || r.status === "running";
        if (!active) {
          // 仅在从 active 变为终态时通知，避免打开历史 run 时弹 toast
          if (prev && (prev === "pending" || prev === "running")) {
            setToast(
              r.status === "completed" ? "机会发现已完成" : r.status === "failed" ? "机会发现失败" : "机会发现已停止",
              r.status === "completed" ? "good" : "warn",
            );
          }
          return; // 停止轮询
        }
        timer = window.setTimeout(poll, POLL_INTERVAL_MS);
      } catch (err) {
        if (!mounted) return;
        const status = (err as ApiError).status;
        setError(status === 404 ? "运行数据不可用（后端尚未实现 404）。" : friendlyError(err));
        setLoading(false);
        // 停止轮询，不无限重试
      }
    }

    void poll();
    return () => {
      mounted = false;
      if (timer) window.clearTimeout(timer);
    };
  }, [api, runId, setToast]);

  // 默认选中 / 失效回退：currentStepId → running → 第一个
  useEffect(() => {
    if (steps.length === 0) {
      if (selectedStepId) setSelectedStepId(null);
      return;
    }
    if (selectedStepId && steps.some((s) => s.id === selectedStepId)) return;
    const current = steps.find((s) => !!run?.currentStepId && s.id === run.currentStepId);
    const running = steps.find((s) => s.status === "running");
    const target = current || running || steps[0];
    if (target?.id) setSelectedStepId(target.id);
  }, [steps, run?.currentStepId, selectedStepId]);

  const selectedStep = steps.find((s) => s.id === selectedStepId) || null;
  const currentStepTitle = (() => {
    const cur = steps.find((s) => !!run?.currentStepId && s.id === run.currentStepId);
    return cur ? `${stockV2DiscoveryStepLabel(cur.stepKey)}` : null;
  })();

  return (
    <>
      <Drawer title="机会发现运行" subtitle={runId.slice(0, 12)} onClose={onClose} width={1080}>
        {loading ? (
          <p className="text-sm text-[var(--muted)]">加载运行状态…</p>
        ) : error ? (
          <div className="grid gap-2">
            <Notice tone="danger">{error}</Notice>
            <Button onClick={onClose}>关闭</Button>
          </div>
        ) : run ? (
          <div className="grid gap-4">
            <RunStatusBar run={run} currentStepTitle={currentStepTitle} />

            <div className="grid grid-cols-[240px_minmax(0,1fr)] gap-4 max-lg:grid-cols-1">
              <div className="rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-2">
                <StockV2OpportunityStepTimeline
                  steps={steps}
                  selectedStepId={selectedStepId}
                  onSelect={setSelectedStepId}
                />
              </div>
              <div className="min-w-0">
                <StepDetail
                  step={selectedStep}
                  run={run}
                  onOpenAgentRun={() => run.agentRunId && setAgentRunDrawerId(run.agentRunId)}
                />
              </div>
            </div>
          </div>
        ) : (
          <p className="text-sm text-[var(--muted)]">未找到运行数据。</p>
        )}
      </Drawer>

      {agentRunDrawerId ? (
        <StockV2AgentRunDetailDrawer
          actions={actions}
          runId={agentRunDrawerId}
          onClose={() => setAgentRunDrawerId(null)}
        />
      ) : null}
    </>
  );
}

function RunStatusBar({
  run,
  currentStepTitle,
}: {
  run: StockV2OpportunityDiscoveryRun;
  currentStepTitle: string | null;
}) {
  const cells: Array<[string, React.ReactNode]> = [
    ["状态", <Pill tone={stockV2DiscoveryRunStatusTone(run.status)}>{stockV2DiscoveryRunStatusLabel(run.status)}</Pill>],
    ["当前步骤", currentStepTitle || "-"],
    ["进度", `${run.stepCompleted ?? 0}/${run.stepTotal ?? 8}`],
    ["候选", run.candidateCount ?? 0],
    ["证据", run.evidenceCount ?? 0],
    ["外部来源", run.externalSourceCount ?? 0],
    ["耗时", formatDuration(run)],
  ];
  return (
    <div className="grid grid-cols-2 gap-2 rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3 text-xs sm:grid-cols-4 lg:grid-cols-7">
      {cells.map(([label, value]) => (
        <div key={label} className="min-w-0">
          <div className="text-[var(--muted)]">{label}</div>
          <div className="mt-0.5 break-words font-medium text-[var(--text)]">{value}</div>
        </div>
      ))}
      {run.errorMessage ? (
        <div className="col-span-full text-[var(--danger)]">错误：{run.errorMessage}</div>
      ) : null}
    </div>
  );
}

function StepDetail({
  step,
  run,
  onOpenAgentRun,
}: {
  step: StockV2OpportunityDiscoveryStep | null;
  run: StockV2OpportunityDiscoveryRun;
  onOpenAgentRun: () => void;
}) {
  if (!step) {
    return (
      <div className="rounded-lg border border-dashed border-[var(--line)] bg-[var(--surface-soft)] p-4 text-xs text-[var(--muted)]">
        步骤数据尚未回填。运行启动后，Agent 会通过 MCP 逐步记录输入、输出与外部来源。
      </div>
    );
  }
  return (
    <div className="grid gap-3 text-sm">
      <div className="flex flex-wrap items-center gap-2">
        <strong>{stockV2DiscoveryStepLabel(step.stepKey)}</strong>
        <Pill tone={step.status === "completed" ? "good" : step.status === "running" ? "warn" : step.status === "failed" ? "danger" : "neutral"}>
          {stockV2DiscoveryStepStatusLabel(step.status)}
        </Pill>
      </div>

      {step.inputSummary ? <Block title="输入摘要" value={step.inputSummary} /> : null}
      {step.outputSummary ? <Block title="输出摘要" value={step.outputSummary} /> : null}

      {step.metadata && Object.keys(step.metadata).length > 0 ? (
        <JSONBlock title="MCP 调用 / 外部来源 / 候选变化" value={step.metadata} />
      ) : null}

      <div className="grid gap-1.5 rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3 text-xs">
        <Row label="开始" value={step.startedAt || "-"} />
        <Row label="结束" value={step.finishedAt || "-"} />
        <Row label="Step ID" value={step.id} mono />
      </div>

      <div className="flex flex-wrap justify-end gap-2 border-t border-[var(--line)] pt-3">
        <Button onClick={onOpenAgentRun} disabled={!run.agentRunId} title={run.agentRunId ? "查看原始 CLI transcript" : "尚未关联 AgentRun"}>
          <ArrowSquareOut size={14} className="mr-1.5" />
          查看 Agent 运行详情
        </Button>
      </div>
    </div>
  );
}

function Block({ title, value }: { title: string; value: string }) {
  return (
    <div>
      <strong className="text-sm">{title}</strong>
      <pre className="mt-1.5 max-h-48 overflow-auto whitespace-pre-wrap rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3 text-xs text-[var(--muted-strong)]">
        {value}
      </pre>
    </div>
  );
}

function JSONBlock({ title, value }: { title: string; value: unknown }) {
  return (
    <details className="rounded-lg border border-[var(--line)] bg-[var(--surface-soft)]" open>
      <summary className="flex cursor-pointer items-center gap-2 px-3 py-2 text-sm font-medium">
        <MagnifyingGlass size={14} />
        {title}
      </summary>
      <pre className="max-h-72 overflow-auto whitespace-pre-wrap border-t border-[var(--line)] px-3 py-3 text-xs text-[var(--muted-strong)]">
        {stringifyJSON(value)}
      </pre>
    </details>
  );
}

function Row({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="grid grid-cols-[64px_minmax(0,1fr)] gap-2">
      <span className="text-[var(--muted)]">{label}</span>
      <span className={`break-words text-[var(--muted-strong)] ${mono ? "font-mono" : ""}`}>{value}</span>
    </div>
  );
}

function formatDuration(run: StockV2OpportunityDiscoveryRun): string {
  if (!run.startedAt || run.startedAt.startsWith("0001")) return "-";
  const start = new Date(run.startedAt).getTime();
  const finishedAt = run.finishedAt && !run.finishedAt.startsWith("0001") ? run.finishedAt : null;
  const end = finishedAt ? new Date(finishedAt).getTime() : Date.now();
  const ms = Math.max(0, end - start);
  if (ms < 60000) return `${Math.floor(ms / 1000)}s`;
  const min = Math.floor(ms / 60000);
  const sec = Math.floor((ms % 60000) / 1000);
  return `${min}m${sec}s`;
}

function stringifyJSON(value: unknown): string {
  try {
    return JSON.stringify(value, null, 2);
  } catch {
    return String(value);
  }
}
