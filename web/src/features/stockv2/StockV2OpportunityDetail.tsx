import { useEffect, useMemo, useState } from "react";
import { ArrowSquareOut, Lightning } from "@phosphor-icons/react";
import type { AppActions } from "../../app/App";
import type {
  ApiError,
  StockV2AgentListResponse,
  StockV2Opportunity,
  StockV2OpportunityCandidate,
  StockV2OpportunityDiscoveryRun,
} from "../../app/types";
import { friendlyError } from "../../api/client";
import { Button, EmptyState, Notice, Pill } from "../../components/ui";
import {
  formatDate,
  stockV2DiscoveryRunStatusLabel,
  stockV2DiscoveryRunStatusTone,
  stockV2OpportunityInstrumentScopeLabel,
  stockV2OpportunityMarketScopeLabel,
  stockV2OpportunityStatusLabel,
  stockV2OpportunityStatusTone,
} from "../../domain/labels";
import { StockV2OpportunityCandidateTable } from "./StockV2OpportunityCandidateTable";
import { StockV2OpportunityCandidateDrawer } from "./StockV2OpportunityCandidateDrawer";

// 单个 Opportunity 详情：header + 「开始发现」+ 候选池（跟随最近 run）+ 发现历史。
export function StockV2OpportunityDetail({
  actions,
  opportunityId,
  refreshToken,
  onOpenRun,
}: {
  actions: AppActions;
  opportunityId: string;
  refreshToken: number;
  onOpenRun: (runId: string) => void;
}) {
  const [opp, setOpp] = useState<StockV2Opportunity | null>(null);
  const [runs, setRuns] = useState<StockV2OpportunityDiscoveryRun[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [starting, setStarting] = useState(false);
  const [candidateDrawer, setCandidateDrawer] = useState<StockV2OpportunityCandidate | null>(null);

  async function load(showLoading = false) {
    if (showLoading) setLoading(true);
    if (showLoading) setError(null);
    try {
      const [o, r] = await Promise.all([
        actions.api<StockV2Opportunity>(`/api/stockv2/opportunities/${encodeURIComponent(opportunityId)}`),
        actions.api<StockV2AgentListResponse<StockV2OpportunityDiscoveryRun>>(
          `/api/stockv2/opportunities/${encodeURIComponent(opportunityId)}/discovery-runs?limit=20`,
        ),
      ]);
      setOpp(o);
      setRuns(r.items || []);
    } catch (err) {
      const status = (err as ApiError).status;
      const message = status === 404 ? "该能力后端尚未实现（404），Embedding 状态区可正常使用。" : friendlyError(err);
      if (showLoading) {
        setError(message);
        setOpp(null);
        setRuns([]);
      } else {
        actions.setToast(`机会详情刷新失败：${message}`, "warn");
      }
    } finally {
      if (showLoading) setLoading(false);
    }
  }

  useEffect(() => {
    void load(true);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [opportunityId]);

  useEffect(() => {
    if (refreshToken === 0 || opp?.id !== opportunityId) return;
    void load(false);
    // Refresh completed run counters without remounting the detail workspace.
    // Remounting collapses the candidate table and moves the browser scroll anchor.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [refreshToken]);

  async function startDiscovery() {
    if (!opp) return;
    setStarting(true);
    try {
      const res = await actions.api<StockV2OpportunityDiscoveryRun>(
        `/api/stockv2/opportunities/${encodeURIComponent(opp.id)}/discovery-runs`,
        { method: "POST", body: {} },
      );
      actions.setToast("机会发现已启动", "good");
      if (res?.id) onOpenRun(res.id);
      void load();
    } catch (err) {
      actions.setToast(friendlyError(err), "danger");
    } finally {
      setStarting(false);
    }
  }

  // runs 按 startedAt/createdAt 倒序，running 优先；候选池跟随最近一条。
  const sortedRuns = useMemo(() => {
    return [...runs].sort((a, b) => {
      const ar = a.status === "running" ? 1 : 0;
      const br = b.status === "running" ? 1 : 0;
      if (ar !== br) return br - ar;
      const ta = new Date(a.startedAt || a.createdAt).getTime();
      const tb = new Date(b.startedAt || b.createdAt).getTime();
      return tb - ta;
    });
  }, [runs]);

  const latestRun = sortedRuns[0] || null;
  const candidateRunId = latestRun?.id || null;

  if (loading) {
    return <p className="text-sm text-[var(--muted)]">加载机会详情…</p>;
  }
  if (error) {
    return (
      <div className="grid gap-2">
        <Notice tone="danger">{error}</Notice>
        <Button onClick={() => void load()}>重试</Button>
      </div>
    );
  }
  if (!opp) {
    return <EmptyState title="未找到机会" body="该机会可能已被删除。" />;
  }

  return (
    <div className="grid gap-4">
      <div className="rounded-lg border border-[var(--line)] bg-[var(--surface)] p-4">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div className="min-w-0">
            <div className="flex flex-wrap items-center gap-2">
              <h2 className="m-0 text-base font-semibold">{opp.title}</h2>
              <Pill tone={stockV2OpportunityStatusTone(opp.status)}>{stockV2OpportunityStatusLabel(opp.status)}</Pill>
            </div>
            {opp.userThesis ? <p className="mt-2 mb-0 text-sm text-[var(--muted-strong)]">{opp.userThesis}</p> : null}
            <div className="mt-2 flex flex-wrap items-center gap-2 text-xs text-[var(--muted)]">
              <span>{stockV2OpportunityMarketScopeLabel(opp.marketScope)}</span>
              <span>·</span>
              <span>{stockV2OpportunityInstrumentScopeLabel(opp.instrumentScope)}</span>
              <span>·</span>
              <span>{formatDate(opp.createdAt) || "-"}</span>
            </div>
          </div>
          <Button tone="primary" disabled={starting} onClick={() => void startDiscovery()}>
            <Lightning size={14} className="mr-1.5" />
            {starting ? "启动中…" : "开始发现"}
          </Button>
        </div>
      </div>

      {/* 候选池：跟随最近一次 run */}
      <div className="min-w-0 overflow-hidden rounded-lg border border-[var(--line)] bg-[var(--surface)] p-4">
        <div className="mb-3 flex items-center justify-between gap-2">
          <strong className="text-sm">候选池</strong>
          {latestRun ? (
            <button
              type="button"
              className="inline-flex items-center gap-1 text-xs text-[var(--accent)] hover:underline"
              onClick={() => latestRun.id && onOpenRun(latestRun.id)}
            >
              <ArrowSquareOut size={12} />
              运行 {latestRun.id.slice(0, 8)} · {stockV2DiscoveryRunStatusLabel(latestRun.status)}
            </button>
          ) : null}
        </div>
        {candidateRunId ? (
          <StockV2OpportunityCandidateTable
            actions={actions}
            runId={candidateRunId}
            onOpenCandidate={(c) => setCandidateDrawer(c)}
          />
        ) : (
          <EmptyState title="尚无候选" body="点击「开始发现」启动一次研究，候选将在主程序校验后出现在这里。" />
        )}
      </div>

      {/* 发现历史 */}
      <div className="rounded-lg border border-[var(--line)] bg-[var(--surface)] p-4">
        <strong className="text-sm">发现历史</strong>
        {sortedRuns.length === 0 ? (
          <p className="mt-2 text-xs text-[var(--muted)]">尚未执行过机会发现。</p>
        ) : (
          <div className="mt-2 grid gap-1.5">
            {sortedRuns.map((run) => (
              <button
                type="button"
                key={run.id}
                onClick={() => onOpenRun(run.id)}
                className="flex flex-wrap items-center justify-between gap-2 rounded-md border border-[var(--line)] bg-[var(--surface-soft)] px-3 py-2 text-left text-xs transition hover:bg-[var(--surface-strong)]"
              >
                <div className="flex flex-wrap items-center gap-2">
                  <Pill tone={stockV2DiscoveryRunStatusTone(run.status)}>{stockV2DiscoveryRunStatusLabel(run.status)}</Pill>
                  <span className="text-[var(--muted-strong)]">
                    {run.stepCompleted ?? 0}/{run.stepTotal ?? 8} 步 · 候选 {run.candidateCount ?? 0} · 证据 {run.evidenceCount ?? 0} · 外部 {run.externalSourceCount ?? 0}
                  </span>
                </div>
                <span className="text-[var(--muted)]">{formatDate(run.startedAt || run.createdAt) || "-"}</span>
              </button>
            ))}
          </div>
        )}
      </div>

      {candidateDrawer ? (
        <StockV2OpportunityCandidateDrawer
          actions={actions}
          candidate={candidateDrawer}
          onClose={() => setCandidateDrawer(null)}
        />
      ) : null}
    </div>
  );
}
