import { useEffect, useState } from "react";
import { ArrowSquareOut, Eye, Prohibit, Sparkle } from "@phosphor-icons/react";
import type { AppActions } from "../../app/App";
import type {
  ApiError,
  StockV2AgentListResponse,
  StockV2AgentRun,
  StockV2OpportunityCandidate,
  StockV2OpportunityCandidateStatus,
} from "../../app/types";
import { friendlyError } from "../../api/client";
import { Button, EmptyState, Notice, Pill, useDangerConfirm } from "../../components/ui";
import {
  stockV2CandidateRelationTypeLabel,
  stockV2CandidateRelationTypeTone,
  stockV2CandidateStatusLabel,
  stockV2CandidateStatusTone,
} from "../../domain/labels";
import { StockV2AgentRunDetailDrawer } from "./StockV2AgentExecutionLedger";
import { SimplePager } from "./StockV2EmbeddingStatusSection";

const PAGE_SIZE = 10;

// 候选池表：rank / 股票·ETF / 关系 / 相关性 / 证据强度 / 市场风险 / 置信度 / 证据数 / 状态 / 操作。
// 操作：生成策略（POST generate-strategy → AgentRun）/ 标记排除（PATCH）/ 查看证据（onOpenCandidate）。
export function StockV2OpportunityCandidateTable({
  actions,
  runId,
  onOpenCandidate,
}: {
  actions: AppActions;
  runId: string;
  onOpenCandidate: (candidate: StockV2OpportunityCandidate) => void;
}) {
  const [items, setItems] = useState<StockV2OpportunityCandidate[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [updatingId, setUpdatingId] = useState<string | null>(null);
  const [strategyRunId, setStrategyRunId] = useState<string | null>(null);
  const { confirmDanger, dangerConfirmDialog } = useDangerConfirm();

  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE));

  async function load(nextPage = page) {
    setLoading(true);
    setError(null);
    try {
      const params = new URLSearchParams({
        limit: String(PAGE_SIZE),
        offset: String((Math.max(1, nextPage) - 1) * PAGE_SIZE),
      });
      const res = await actions.api<StockV2AgentListResponse<StockV2OpportunityCandidate>>(
        `/api/stockv2/opportunity-discovery-runs/${encodeURIComponent(runId)}/candidates?${params}`,
      );
      setItems(res.items || []);
      setTotal(res.total ?? res.items?.length ?? 0);
    } catch (err) {
      const status = (err as ApiError).status;
      setError(status === 404 ? "候选池暂不可用（后端尚未实现 404）。" : friendlyError(err));
      setItems([]);
      setTotal(0);
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    void load(1);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [runId]);

  useEffect(() => {
    void load(page);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [page]);

  async function generateStrategy(c: StockV2OpportunityCandidate) {
    setUpdatingId(c.id);
    try {
      const res = await actions.api<StockV2AgentRun>(
        `/api/stockv2/opportunity-candidates/${encodeURIComponent(c.id)}/generate-strategy`,
        { method: "POST", body: {} },
      );
      actions.setToast("策略生成已启动，完成后进入策略草案列表", "good");
      setStrategyRunId(res.id);
      void load(page);
    } catch (err) {
      actions.setToast(friendlyError(err), "danger");
    } finally {
      setUpdatingId(null);
    }
  }

  async function reject(c: StockV2OpportunityCandidate) {
    const ok = await confirmDanger({
      title: "标记排除该候选",
      body: `将把 ${c.symbol}${c.name ? ` ${c.name}` : ""} 标记为已排除。`,
      confirmLabel: "标记排除",
    });
    if (!ok) return;
    setUpdatingId(c.id);
    try {
      await actions.api<StockV2OpportunityCandidate>(
        `/api/stockv2/opportunity-candidates/${encodeURIComponent(c.id)}`,
        { method: "PATCH", body: { status: "rejected" as StockV2OpportunityCandidateStatus } },
      );
      actions.setToast("已标记排除", "good");
      void load(page);
    } catch (err) {
      actions.setToast(friendlyError(err), "danger");
    } finally {
      setUpdatingId(null);
    }
  }

  if (error) {
    return (
      <div className="grid gap-2">
        <Notice tone="danger">{error}</Notice>
        <Button onClick={() => void load(page)}>重试</Button>
      </div>
    );
  }

  if (!loading && items.length === 0) {
    return <EmptyState title="候选池为空" body="运行完成后，主程序校验通过的候选会出现在这里。" />;
  }

  return (
    <>
      <div className="overflow-x-auto">
        <table className="w-full min-w-[860px] border-collapse text-xs">
          <thead>
            <tr className="border-b border-[var(--line)] text-left text-[var(--muted)]">
              <Th>#</Th>
              <Th>股票 / ETF</Th>
              <Th>关系</Th>
              <Th>相关性</Th>
              <Th>证据强度</Th>
              <Th>市场风险</Th>
              <Th>置信度</Th>
              <Th>证据数</Th>
              <Th>状态</Th>
              <Th>操作</Th>
            </tr>
          </thead>
          <tbody>
            {items.map((c) => {
              const strategyStarted = c.status === "strategy_requested" || c.status === "strategy_generated";
              const busy = updatingId === c.id;
              return (
                <tr key={c.id} className="border-b border-[var(--line)] align-middle">
                  <Td><span className="font-mono text-[var(--muted-strong)]">{c.rank ?? "-"}</span></Td>
                  <Td>
                    <button
                      type="button"
                      className="text-left hover:underline"
                      onClick={() => onOpenCandidate(c)}
                      title="查看候选详情"
                    >
                      <span className="font-mono font-medium text-[var(--text)]">{c.symbol}</span>
                      {c.name ? <span className="ml-1.5 text-[var(--muted-strong)]">{c.name}</span> : null}
                      {c.market ? <span className="ml-1.5 text-[var(--muted)]">{c.market}</span> : null}
                    </button>
                  </Td>
                  <Td>
                    <Pill tone={stockV2CandidateRelationTypeTone(c.relationType)}>
                      {stockV2CandidateRelationTypeLabel(c.relationType)}
                    </Pill>
                  </Td>
                  <Td><ScoreBar value={c.relevanceScore} good /></Td>
                  <Td><ScoreBar value={c.evidenceScore} good /></Td>
                  <Td><ScoreBar value={c.marketRiskScore} risk /></Td>
                  <Td>
                    <span className="text-[var(--muted-strong)]">
                      {typeof c.confidence === "number" ? `${Math.round(c.confidence * 100)}%` : "-"}
                    </span>
                  </Td>
                  <Td><span className="text-[var(--muted-strong)]">{c.evidenceCount ?? "-"}</span></Td>
                  <Td>
                    <Pill tone={stockV2CandidateStatusTone(c.status)}>{stockV2CandidateStatusLabel(c.status)}</Pill>
                  </Td>
                  <Td>
                    <div className="flex items-center gap-1">
                      <Button
                        tone={strategyStarted ? "neutral" : "primary"}
                        disabled={busy}
                        title={strategyStarted ? "已请求策略生成" : "从该候选生成策略草案"}
                        onClick={() => void generateStrategy(c)}
                      >
                        <Sparkle size={12} className="mr-1" />
                        {strategyStarted ? "已生成" : "策略"}
                      </Button>
                      <Button disabled={busy || c.status === "rejected"} title="查看证据" onClick={() => onOpenCandidate(c)}>
                        <Eye size={12} />
                      </Button>
                      <Button disabled={busy || c.status === "rejected"} title="标记排除" onClick={() => void reject(c)}>
                        <Prohibit size={12} />
                      </Button>
                    </div>
                  </Td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>

      {loading ? <p className="mt-2 text-xs text-[var(--muted)]">加载中…</p> : null}

      <div className="mt-2">
        <SimplePager loading={loading} page={page} pageSize={PAGE_SIZE} total={total} totalPages={totalPages} onPage={setPage} />
      </div>

      {strategyRunId ? (
        <div className="mt-3 rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3 text-xs">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <span className="text-[var(--muted-strong)]">策略生成运行已启动（{strategyRunId.slice(0, 8)}）</span>
            <Button onClick={() => setStrategyRunId(strategyRunId)}>
              <ArrowSquareOut size={12} className="mr-1" />
              查看 Agent 运行
            </Button>
          </div>
        </div>
      ) : null}

      {strategyRunId ? (
        <StockV2AgentRunDetailDrawer
          actions={actions}
          runId={strategyRunId}
          onClose={() => setStrategyRunId(null)}
        />
      ) : null}

      {dangerConfirmDialog}
    </>
  );
}

function Th({ children }: { children: React.ReactNode }) {
  return <th className="whitespace-nowrap px-2 py-2 font-medium">{children}</th>;
}

function Td({ children }: { children: React.ReactNode }) {
  return <td className="px-2 py-2">{children}</td>;
}

// 轻量分数条。good=true 时高分绿；risk=true 时高分红（市场风险越高越红）。
function ScoreBar({ value, good, risk }: { value?: number; good?: boolean; risk?: boolean }) {
  const num = typeof value === "number" ? Math.max(0, Math.min(100, value)) : null;
  if (num === null) return <span className="text-[var(--muted)]">-</span>;
  const pct = Math.round(num);
  const color = risk
    ? pct >= 67 ? "var(--danger)" : pct >= 34 ? "var(--warn)" : "var(--good)"
    : good
      ? pct >= 67 ? "var(--good)" : pct >= 34 ? "var(--warn)" : "var(--danger)"
      : "var(--accent)";
  return (
    <div className="flex items-center gap-1.5">
      <div className="h-1.5 w-10 overflow-hidden rounded-full bg-[var(--surface-strong)]">
        <div className="h-full rounded-full" style={{ width: `${pct}%`, background: color }} />
      </div>
      <span className="text-[var(--muted-strong)]">{num}</span>
    </div>
  );
}
