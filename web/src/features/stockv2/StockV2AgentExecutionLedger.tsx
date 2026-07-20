import { MagnifyingGlass, TerminalWindow } from "@phosphor-icons/react";
import { useEffect, useMemo, useState, type ReactNode } from "react";
import type { AppActions } from "../../app/App";
import type {
  StockV2AgentExecutionDetail,
  StockV2AgentListResponse,
  StockV2AgentRun,
} from "../../app/types";
import { friendlyError } from "../../api/client";
import { Button, CollapsibleSection, ContextList, CopyButton, Drawer, Notice, Pill } from "../../components/ui";
import {
  formatDate,
  stockV2AgentRunStatusLabel,
  stockV2AgentRunStatusTone,
  stockV2AgentTaskTypeLabel,
} from "../../domain/labels";

const AGENT_RUN_PAGE_SIZE = 10;

export function StockV2AgentExecutionLedgerSection({ actions }: { actions: AppActions }) {
  const [items, setItems] = useState<StockV2AgentRun[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [loading, setLoading] = useState(false);
  const [started, setStarted] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [detailRunId, setDetailRunId] = useState<string | null>(null);
  const totalPages = Math.max(1, Math.ceil(total / AGENT_RUN_PAGE_SIZE));
  const pageNumbers = useMemo(() => paginationWindow(page, totalPages), [page, totalPages]);

  async function load(nextPage = page) {
    setLoading(true);
    setError(null);
    try {
      const params = new URLSearchParams({
        limit: String(AGENT_RUN_PAGE_SIZE),
        offset: String((Math.max(1, nextPage) - 1) * AGENT_RUN_PAGE_SIZE),
      });
      const res = await actions.api<StockV2AgentListResponse<StockV2AgentRun>>(
        `/api/stockv2/agent/runs?${params}`,
      );
      setItems(res.items || []);
      setTotal(res.total ?? res.items?.length ?? 0);
    } catch (err) {
      setError(friendlyError(err));
      setItems([]);
      setTotal(0);
    } finally {
      setLoading(false);
    }
  }

  function openSection() {
    if (!started) {
      setStarted(true);
      void load(1);
    }
  }

  useEffect(() => {
    if (started) void load(page);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [page]);

  return (
    <CollapsibleSection
      title="Agent 执行台账"
      subtitle={total > 0 ? `${total} 次 Agent 运行 · stdout / MCP 回填 / Review 上下文` : "按页查看 AgentRun 与 DecisionLedger"}
      defaultOpen={false}
    >
      <div onClick={openSection}>
        {loading || !started ? (
          <p className="text-sm text-[var(--muted)]">加载 Agent 执行台账…</p>
        ) : error ? (
          <div className="grid gap-2">
            <Notice tone="danger">{error}</Notice>
            <Button onClick={() => void load(page)}>重试</Button>
          </div>
        ) : items.length === 0 ? (
          <p className="text-sm text-[var(--muted)]">暂无 Agent 运行。Review 或 CLI 调试触发后会出现在这里。</p>
        ) : (
          <>
            <StockV2AgentExecutionSummaryList
              items={items.map((run) => ({ run }))}
              onOpen={(runId) => setDetailRunId(runId)}
            />
            <AgentPagination
              loading={loading}
              page={page}
              pageNumbers={pageNumbers}
              pageSize={AGENT_RUN_PAGE_SIZE}
              total={total}
              totalPages={totalPages}
              onPage={setPage}
              label="Agent 运行页码"
            />
          </>
        )}
      </div>
      {detailRunId ? (
        <StockV2AgentRunDetailDrawer actions={actions} runId={detailRunId} onClose={() => setDetailRunId(null)} />
      ) : null}
    </CollapsibleSection>
  );
}

export function StockV2AgentExecutionSummaryList({
  items,
  onOpen,
}: {
  items: StockV2AgentExecutionDetail[];
  onOpen: (runId: string) => void;
}) {
  if (items.length === 0) {
    return (
      <div className="rounded-lg border border-dashed border-[var(--line)] bg-[var(--surface-soft)] p-4 text-center text-xs text-[var(--muted)]">
        未触发 Agent。
      </div>
    );
  }
  return (
    <div className="grid gap-2">
      {items.map((detail) => (
        <button
          type="button"
          key={detail.run.id}
          className="w-full rounded-lg border border-[var(--line)] bg-[var(--surface)] p-3 text-left text-xs transition hover:bg-[var(--surface-soft)]"
          onClick={() => onOpen(detail.run.id)}
        >
          <div className="flex flex-wrap items-center gap-2">
            <TerminalWindow size={14} />
            <strong className="text-sm">{agentRunTitle(detail)}</strong>
            <Pill tone={stockV2AgentRunStatusTone(detail.run.status)}>
              {stockV2AgentRunStatusLabel(detail.run.status)}
            </Pill>
            <span className="font-mono text-[var(--muted-strong)]">model {detail.run.modelId?.slice(0, 8) || "-"}</span>
          </div>
          {detail.run.output ? (
            <p className="mt-1 break-words text-[var(--muted-strong)]">{detail.run.output}</p>
          ) : detail.run.errorMessage ? (
            <p className="mt-1 break-words text-[var(--danger)]">{detail.run.errorMessage}</p>
          ) : null}
          <div className="mt-2 flex flex-wrap items-center gap-x-4 gap-y-1 text-[var(--muted)]">
            <span>{formatDate(detail.run.startedAt || detail.run.createdAt) || "-"}</span>
            <span>{detail.run.triggerObjectType || "-"}:{detail.run.triggerObjectId?.slice(0, 8) || "-"}</span>
            <span>{detail.run.executionMode === "api" ? "API" : "CLI"} · 推理 {detail.run.reasoningEffort || "模型默认"}</span>
            {detail.ledger ? <span>Ledger {detail.ledger.id.slice(0, 8)}</span> : null}
            <span className="text-[var(--accent)]">查看执行上下文</span>
          </div>
        </button>
      ))}
    </div>
  );
}

export function StockV2AgentRunDetailDrawer({
  actions,
  runId,
  onClose,
}: {
  actions: AppActions;
  runId: string;
  onClose: () => void;
}) {
  const [detail, setDetail] = useState<StockV2AgentExecutionDetail | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let mounted = true;
    async function load() {
      setLoading(true);
      setError(null);
      try {
        const res = await actions.api<StockV2AgentExecutionDetail>(
          `/api/stockv2/agent/runs/${runId}/detail`,
        );
        if (mounted) setDetail(res);
      } catch (err) {
        if (mounted) setError(friendlyError(err));
      } finally {
        if (mounted) setLoading(false);
      }
    }
    void load();
    return () => {
      mounted = false;
    };
  }, [actions, runId]);

  return (
    <Drawer title="Agent 执行上下文" subtitle={runId.slice(0, 12)} onClose={onClose} width={760}>
      {loading ? (
        <p className="text-sm text-[var(--muted)]">加载 Agent 执行详情…</p>
      ) : error ? (
        <Notice tone="danger">{error}</Notice>
      ) : detail ? (
        <StockV2AgentRunDetailPanel detail={detail} />
      ) : (
        <p className="text-sm text-[var(--muted)]">未找到执行详情。</p>
      )}
    </Drawer>
  );
}

export function StockV2AgentRunDetailPanel({ detail }: { detail: StockV2AgentExecutionDetail }) {
  const { run, ledger, inputContext, review } = detail;
  const identityItems: Array<[string, ReactNode]> = [
    ["开始时间", formatDate(run.startedAt || run.createdAt) || "-"],
    ["结束时间", formatAgentRunFinishedAt(run)],
    ["Run ID", <span className="font-mono">{run.id}</span>],
    ["Ledger ID", <span className="font-mono">{run.decisionLedgerId || ledger?.id || "-"}</span>],
    ["执行模式", <span className="font-mono">{run.executionMode === "api" ? "API" : "CLI"}</span>],
    ["推理强度", <span className="font-mono">{run.reasoningEffort || "模型默认（未覆盖）"}</span>],
  ];
  if (review) identityItems.push(["Review", `${review.status || "-"} · ${review.id}`]);
  return (
    <div className="grid gap-4 text-sm">
      <div className="grid grid-cols-2 gap-2">
        <SummaryCell label="运行状态">
          <Pill tone={stockV2AgentRunStatusTone(run.status)}>{stockV2AgentRunStatusLabel(run.status)}</Pill>
        </SummaryCell>
        <SummaryCell label="任务类型">{stockV2AgentTaskTypeLabel(run.taskType)}</SummaryCell>
        <SummaryCell label="模型"><span className="font-mono">{run.modelId?.slice(0, 12) || "-"}</span></SummaryCell>
        <SummaryCell label="触发对象"><span className="font-mono">{`${run.triggerObjectType || "-"}:${run.triggerObjectId?.slice(0, 8) || "-"}`}</span></SummaryCell>
      </div>

      {run.output ? <Block title="运行结论" value={run.output} /> : null}
      {run.errorMessage ? <Block title="运行错误" value={run.errorMessage} danger /> : null}
      {detail.strategyGenerationSteps?.length ? <StrategyGenerationPipelineDetail detail={detail} /> : null}

      <ContextList items={identityItems} />

      {ledger ? (
        <>
          <Block title="输入摘要" value={ledger.inputSummary || "(空)"} />
          {ledger.prompt ? <Block title="Prompt" value={ledger.prompt} mono /> : null}
          {ledger.outputArtifactSummary ? <Block title="执行输出 stdout/stderr" value={ledger.outputArtifactSummary} mono /> : null}
          <JSONBlock title="MCP 回填结果" value={ledger.structuredOutput || {}} />
          <JSONBlock title="脱敏摘要" value={ledger.redactionSummary || {}} />
        </>
      ) : (
        <Notice tone="warn">本次运行没有关联 DecisionLedger。</Notice>
      )}

      {inputContext ? <JSONBlock title="Review 输入上下文" value={inputContext} /> : null}
    </div>
  );
}

function StrategyGenerationPipelineDetail({ detail }: { detail: StockV2AgentExecutionDetail }) {
  const contexts = detail.strategyGenerationContexts || [];
  return (
    <div className="grid gap-3 rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3">
      <div>
        <strong className="text-sm">策略生成流程</strong>
        <p className="mt-0.5 text-xs text-[var(--muted)]">多角色 Codex CLI 调用、证据校验、组合裁决和最终结构化草案。</p>
      </div>
      <div className="grid gap-2">
        {(detail.strategyGenerationSteps || []).map((step) => {
          const stepContexts = contexts.filter((item) => item.stepId === step.id);
          return (
            <details key={step.id} className="rounded-lg border border-[var(--line)] bg-[var(--surface)]" open={step.status === "failed" || step.stepKey === "strategy_formatter"}>
              <summary className="cursor-pointer px-3 py-2">
                <div className="flex flex-wrap items-center gap-2 text-xs">
                  <span className="flex h-5 w-5 items-center justify-center rounded-full border border-[var(--line)] font-mono">{step.sequenceNo}</span>
                  <strong className="text-sm">{step.stepName || step.stepKey}</strong>
                  <Pill tone={agentStepTone(step.status)}>{agentStepStatusLabel(step.status)}</Pill>
                  <span className="font-mono text-[var(--muted)]">{step.role}</span>
                  <span className="text-[var(--muted)]">{formatDate(step.startedAt || step.createdAt) || "-"}</span>
                </div>
                {step.outputSummary ? <p className="mt-1 text-xs text-[var(--muted-strong)]">{step.outputSummary}</p> : null}
                {step.errorMessage ? <p className="mt-1 text-xs text-[var(--danger)]">{step.errorMessage}</p> : null}
              </summary>
              <div className="grid gap-3 border-t border-[var(--line)] p-3">
                {step.inputSummary ? <Block title="步骤目标" value={step.inputSummary} /> : null}
                {step.prompt ? <Block title="Step Prompt" value={step.prompt} mono /> : null}
                {step.outputArtifactSummary ? <Block title="Step stdout/stderr" value={step.outputArtifactSummary} mono /> : null}
                <JSONBlock title="Step MCP 回填" value={step.structuredOutput || {}} />
                {stepContexts.map((ctx) => (
                  <div key={ctx.id} className="grid gap-2">
                    {ctx.contentText ? <Block title={ctx.title || ctx.contextType} value={ctx.contentText} /> : null}
                    <JSONBlock title={`上下文 · ${ctx.title || ctx.contextType}`} value={ctx.contentJson || {}} />
                  </div>
                ))}
              </div>
            </details>
          );
        })}
      </div>
      {contexts.some((item) => !item.stepId) ? (
        <details className="rounded-lg border border-[var(--line)] bg-[var(--surface)]">
          <summary className="cursor-pointer px-3 py-2 text-sm font-medium">全局输入上下文</summary>
          <div className="grid gap-2 border-t border-[var(--line)] p-3">
            {contexts.filter((item) => !item.stepId).map((ctx) => (
              <JSONBlock key={ctx.id} title={ctx.title || ctx.contextType} value={ctx.contentJson || {}} />
            ))}
          </div>
        </details>
      ) : null}
    </div>
  );
}

function agentStepStatusLabel(status: string): string {
  switch (status) {
    case "completed":
      return "完成";
    case "running":
      return "运行中";
    case "failed":
      return "失败";
    case "pending":
      return "等待";
    default:
      return status || "-";
  }
}

function agentStepTone(status: string): "neutral" | "good" | "warn" | "danger" {
  switch (status) {
    case "completed":
      return "good";
    case "running":
      return "warn";
    case "failed":
      return "danger";
    default:
      return "neutral";
  }
}

function AgentPagination({
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
        <Button disabled={loading || page <= 1} onClick={() => onPage(Math.max(1, page - 1))}>上一页</Button>
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
        <Button disabled={loading || page >= totalPages} onClick={() => onPage(Math.min(totalPages, page + 1))}>下一页</Button>
        <select
          aria-label={`选择${label}`}
          className="select h-9 w-24 text-xs"
          disabled={loading}
          onChange={(event) => onPage(Number(event.target.value))}
          value={page}
        >
          {Array.from({ length: totalPages }, (_, idx) => idx + 1).map((item) => (
            <option key={item} value={item}>第 {item} 页</option>
          ))}
        </select>
      </div>
    </div>
  );
}

function formatAgentRunFinishedAt(run: StockV2AgentRun): string {
  if (run.status !== "completed" && run.status !== "failed") return "-";
  if (!run.finishedAt || run.finishedAt.startsWith("0001-01-01")) return "-";
  return formatDate(run.finishedAt) || "-";
}

function SummaryCell({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3">
      <span className="block text-xs text-[var(--muted)]">{label}</span>
      <div className="mt-1.5 text-sm">{children}</div>
    </div>
  );
}

function Block({ title, value, mono, danger }: { title: string; value: string; mono?: boolean; danger?: boolean }) {
  return (
    <div>
      <div className="flex items-center justify-between gap-2">
        <strong className="text-sm">{title}</strong>
        <CopyButton text={value} />
      </div>
      <pre className={`mt-2 max-h-56 overflow-auto whitespace-pre-wrap break-words rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3 text-xs ${mono ? "font-mono" : ""} ${danger ? "text-[var(--danger)]" : "text-[var(--muted-strong)]"}`}>
        {value}
      </pre>
    </div>
  );
}

function JSONBlock({ title, value }: { title: string; value: unknown }) {
  const text = stringifyJSON(value);
  return (
    <details className="rounded-lg border border-[var(--line)] bg-[var(--surface-soft)]">
      <summary className="flex cursor-pointer items-center gap-2 px-3 py-2 text-sm font-medium">
        <MagnifyingGlass size={14} />
        {title}
        <CopyButton text={text} className="ml-auto" />
      </summary>
      <pre className="max-h-72 overflow-auto whitespace-pre-wrap break-words border-t border-[var(--line)] px-3 py-3 text-xs text-[var(--muted-strong)]">
        {text}
      </pre>
    </details>
  );
}

function agentRunTitle(detail: StockV2AgentExecutionDetail): string {
  if (detail.run.triggerObjectType === "agent_cli_debug") return "CLI 链路调试";
  if (detail.review?.symbol) return `Review · ${detail.review.symbol}`;
  return stockV2AgentTaskTypeLabel(detail.run.taskType) || "Agent Run";
}

function paginationWindow(page: number, totalPages: number): Array<number | "ellipsis"> {
  if (totalPages <= 7) {
    return Array.from({ length: totalPages }, (_, idx) => idx + 1);
  }
  const pages = new Set<number>([1, totalPages, page, page - 1, page + 1]);
  if (page <= 3) [2, 3, 4].forEach((item) => pages.add(item));
  if (page >= totalPages - 2) [totalPages - 1, totalPages - 2, totalPages - 3].forEach((item) => pages.add(item));
  const sorted = Array.from(pages)
    .filter((item) => item >= 1 && item <= totalPages)
    .sort((a, b) => a - b);
  const result: Array<number | "ellipsis"> = [];
  sorted.forEach((item) => {
    const previous = result[result.length - 1];
    if (typeof previous === "number" && item - previous > 1) result.push("ellipsis");
    result.push(item);
  });
  return result;
}

function stringifyJSON(value: unknown): string {
  try {
    return JSON.stringify(value, null, 2);
  } catch {
    return String(value);
  }
}
