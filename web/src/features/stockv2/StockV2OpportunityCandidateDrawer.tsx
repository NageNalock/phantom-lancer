import { useEffect, useState } from "react";
import { ArrowSquareOut, Sparkle } from "@phosphor-icons/react";
import type { AppActions } from "../../app/App";
import type {
  ApiError,
  StockV2AgentListResponse,
  StockV2AgentRun,
  StockV2EmbeddingStatus,
  StockV2OpportunityCandidate,
  StockV2OpportunityEvidence,
  StockV2OpportunityEvidenceSourceType,
} from "../../app/types";
import { friendlyError } from "../../api/client";
import { Button, Drawer, Notice, Pill } from "../../components/ui";
import {
  stockV2CandidateRelationTypeLabel,
  stockV2CandidateRelationTypeTone,
  stockV2CandidateStatusLabel,
  stockV2CandidateStatusTone,
  stockV2EvidenceSourceTypeLabel,
  stockV2EmbeddingErrorCodeLabel,
} from "../../domain/labels";
import { StockV2AgentRunDetailDrawer } from "./StockV2AgentExecutionLedger";

const EVIDENCE_GROUP_ORDER: StockV2OpportunityEvidenceSourceType[] = [
  "internal_profile",
  "internal_news",
  "quote",
  "daily_bar",
  "external_source",
  "agent_note",
  "semantic_recall",
];

// 候选详情 Drawer：原因 / 证据分组(含行情·日K·新闻·外部来源·语义召回) / 风险 / 已有策略 / embedding 来源。
// 内部加载该候选的 evidence 与全局 embeddingStatus；available=false 时顶部提示未执行向量召回。
export function StockV2OpportunityCandidateDrawer({
  actions,
  candidate,
  onClose,
}: {
  actions: AppActions;
  candidate: StockV2OpportunityCandidate;
  onClose: () => void;
}) {
  const [evidence, setEvidence] = useState<StockV2OpportunityEvidence[]>([]);
  const [embeddingStatus, setEmbeddingStatus] = useState<StockV2EmbeddingStatus | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [generating, setGenerating] = useState(false);
  const [lastStrategyRunId, setLastStrategyRunId] = useState<string | null>(null);
  const [agentRunDrawerId, setAgentRunDrawerId] = useState<string | null>(null);

  async function load() {
    setLoading(true);
    setError(null);
    try {
      const tasks: Array<Promise<unknown>> = [
        actions.api<StockV2EmbeddingStatus>("/api/stockv2/embeddings/status").then(setEmbeddingStatus).catch(() => undefined),
      ];
      if (candidate.runId) {
        const params = new URLSearchParams({ candidateId: candidate.id, limit: "100" });
        tasks.push(
          actions.api<StockV2AgentListResponse<StockV2OpportunityEvidence>>(
            `/api/stockv2/opportunity-discovery-runs/${encodeURIComponent(candidate.runId)}/evidence?${params}`,
          ).then((res) => setEvidence(res.items || [])),
        );
      }
      await Promise.all(tasks);
    } catch (err) {
      const status = (err as ApiError).status;
      setError(status === 404 ? "证据数据暂不可用（后端尚未实现 404）。" : friendlyError(err));
      setEvidence([]);
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    void load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [candidate.id]);

  async function generateStrategy() {
    setGenerating(true);
    try {
      const res = await actions.api<StockV2AgentRun>(
        `/api/stockv2/opportunity-candidates/${encodeURIComponent(candidate.id)}/generate-strategy`,
        { method: "POST", body: {} },
      );
      actions.setToast("策略生成已启动，完成后进入策略草案列表", "good");
      setLastStrategyRunId(res.id);
      setAgentRunDrawerId(res.id);
    } catch (err) {
      actions.setToast(friendlyError(err), "danger");
    } finally {
      setGenerating(false);
    }
  }

  const embeddingReady = !!embeddingStatus?.available;
  const disabledReason = !embeddingReady
    ? embeddingStatus?.errorMessage || stockV2EmbeddingErrorCodeLabel(embeddingStatus?.errorCode)
    : null;
  const groups = groupEvidence(evidence);
  const strategyStarted = candidate.status === "strategy_requested" || candidate.status === "strategy_generated";

  return (
    <>
      <Drawer
        title={
          <span>
            <span className="font-mono">{candidate.symbol}</span>
            {candidate.name ? <span className="ml-2 font-sans font-normal">{candidate.name}</span> : null}
          </span>
        }
        subtitle={`${stockV2CandidateRelationTypeLabel(candidate.relationType)} · ${candidate.market || ""}`}
        onClose={onClose}
        width={720}
        footer={
          <Button
            tone={strategyStarted ? "neutral" : "primary"}
            disabled={generating || (strategyStarted && !lastStrategyRunId)}
            onClick={() => {
              if (strategyStarted) {
                if (lastStrategyRunId) setAgentRunDrawerId(lastStrategyRunId);
                return;
              }
              void generateStrategy();
            }}
          >
            {strategyStarted && lastStrategyRunId ? <ArrowSquareOut size={14} className="mr-1.5" /> : <Sparkle size={14} className="mr-1.5" />}
            {strategyStarted ? (lastStrategyRunId ? "查看 Agent 运行" : "已生成策略") : generating ? "提交中…" : "生成策略草案"}
          </Button>
        }
      >
        <div className="grid gap-4 text-sm">
          {/* 头部评分 */}
          <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
            <ScoreCell label="相关性" value={candidate.relevanceScore} />
            <ScoreCell label="证据强度" value={candidate.evidenceScore} />
            <ScoreCell label="市场风险" value={candidate.marketRiskScore} risk />
            <ScoreCell label="置信度" value={candidate.confidence} percent />
          </div>
          <div className="flex flex-wrap items-center gap-2 text-xs text-[var(--muted)]">
            <Pill tone={stockV2CandidateRelationTypeTone(candidate.relationType)}>
              {stockV2CandidateRelationTypeLabel(candidate.relationType)}
            </Pill>
            <Pill tone={stockV2CandidateStatusTone(candidate.status)}>{stockV2CandidateStatusLabel(candidate.status)}</Pill>
            {candidate.rank ? <span>排名 #{candidate.rank}</span> : null}
            <span>证据数 {candidate.evidenceCount ?? evidence.length}</span>
          </div>

          {/* embedding 拦截提示 */}
          {embeddingStatus && !embeddingReady ? (
            <Notice tone="warn">本次未执行语义向量召回。原因：{disabledReason}。展示的证据来自关键词、项目内资料与外部搜索。</Notice>
          ) : null}

          {error ? <Notice tone="danger">{error}</Notice> : null}

          {/* 相关原因 */}
          {candidate.reason ? <Block title="为什么相关" value={candidate.reason} /> : null}

          {/* 风险摘要 */}
          {candidate.riskSummary ? <Block title="风险摘要" value={candidate.riskSummary} danger /> : null}

          {/* 证据分组（含行情/日K/新闻/外部来源/语义召回） */}
          <div className="grid gap-3">
            <strong className="text-sm">支持证据</strong>
            {loading ? (
              <p className="text-xs text-[var(--muted)]">加载证据中…</p>
            ) : groups.length === 0 ? (
              <p className="text-xs text-[var(--muted)]">暂无证据记录。{candidate.runId ? "" : "（缺少 runId，无法查询证据）"}</p>
            ) : (
              groups.map((g) => (
                <EvidenceGroup key={g.type} type={g.type} items={g.items} />
              ))
            )}
          </div>

          {/* 已有策略 */}
          <div className="rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3 text-xs">
            <strong className="text-sm">已有策略</strong>
            <p className="mt-1 text-[var(--muted-strong)]">
              {strategyStarted ? "已从该候选发起策略生成，结果在策略草案列表查看。" : "尚无关联策略。可点击下方「生成策略草案」。"}
            </p>
            {lastStrategyRunId ? (
              <div className="mt-2">
                <Button onClick={() => setAgentRunDrawerId(lastStrategyRunId)}>
                  <ArrowSquareOut size={12} className="mr-1" />
                  查看运行 {lastStrategyRunId.slice(0, 8)}
                </Button>
              </div>
            ) : null}
          </div>
        </div>
      </Drawer>

      {agentRunDrawerId ? (
        <StockV2AgentRunDetailDrawer actions={actions} runId={agentRunDrawerId} onClose={() => setAgentRunDrawerId(null)} />
      ) : null}
    </>
  );
}

function EvidenceGroup({
  type,
  items,
}: {
  type: StockV2OpportunityEvidenceSourceType;
  items: StockV2OpportunityEvidence[];
}) {
  const isSemantic = type === "semantic_recall";
  return (
    <div className={`rounded-lg border p-3 text-xs ${isSemantic ? "border-[var(--accent)] bg-[var(--accent-soft)]" : "border-[var(--line)] bg-[var(--surface-soft)]"}`}>
      <div className="flex flex-wrap items-center gap-2">
        <Pill tone={isSemantic ? "neutral" : "neutral"}>{stockV2EvidenceSourceTypeLabel(type)}</Pill>
        <span className="text-[var(--muted)]">{items.length} 条</span>
      </div>
      <div className="mt-2 grid gap-2">
        {items.map((e) => (
          <div key={e.id} className="rounded border border-[var(--line)] bg-[var(--surface)] p-2">
            {e.title ? <div className="font-medium text-[var(--text)]">{e.title}</div> : null}
            {e.summary ? <div className="mt-0.5 text-[var(--muted-strong)]">{e.summary}</div> : null}
            <div className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-0.5 text-[var(--muted)]">
              {e.publisher ? <span>{e.publisher}</span> : null}
              {e.publishedAt ? <span>{e.publishedAt}</span> : null}
              {typeof e.confidence === "number" ? <span>置信 {Math.round(e.confidence * 100)}%</span> : null}
              {e.url ? (
                <a href={e.url} target="_blank" rel="noreferrer" className="inline-flex items-center gap-0.5 text-[var(--accent)] hover:underline">
                  <ArrowSquareOut size={11} /> 来源
                </a>
              ) : null}
            </div>
            {isSemantic ? (
              <div className="mt-1 text-[var(--muted-strong)]">
                向量召回 · 模型 {e.embeddingModelName || e.embeddingModelId || "-"}
                {typeof e.score === "number" ? ` · score ${e.score.toFixed(3)}` : ""}
                {e.assetStatus ? ` · 资产${e.assetStatus}` : ""}
              </div>
            ) : null}
          </div>
        ))}
      </div>
    </div>
  );
}

function ScoreCell({ label, value, risk, percent }: { label: string; value?: number; risk?: boolean; percent?: boolean }) {
  const num = typeof value === "number" ? value : null;
  const display = num === null ? "-" : percent ? `${Math.round(num * 100)}%` : String(Math.round(num));
  const color = num === null
    ? "text-[var(--muted)]"
    : percent
      ? num >= 0.67 ? "text-[var(--good)]" : num >= 0.34 ? "text-[var(--warn)]" : "text-[var(--danger)]"
      : risk
        ? num >= 67 ? "text-[var(--danger)]" : num >= 34 ? "text-[var(--warn)]" : "text-[var(--good)]"
        : num >= 67 ? "text-[var(--good)]" : num >= 34 ? "text-[var(--warn)]" : "text-[var(--danger)]";
  return (
    <div className="rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-2">
      <div className="text-xs text-[var(--muted)]">{label}</div>
      <div className={`mt-0.5 text-lg font-semibold ${color}`}>{display}</div>
    </div>
  );
}

function Block({ title, value, danger }: { title: string; value: string; danger?: boolean }) {
  return (
    <div>
      <strong className="text-sm">{title}</strong>
      <pre className={`mt-1.5 whitespace-pre-wrap rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3 text-xs ${danger ? "text-[var(--danger)]" : "text-[var(--muted-strong)]"}`}>
        {value}
      </pre>
    </div>
  );
}

function groupEvidence(evidence: StockV2OpportunityEvidence[]): Array<{ type: StockV2OpportunityEvidenceSourceType; items: StockV2OpportunityEvidence[] }> {
  const groups = new Map<string, StockV2OpportunityEvidence[]>();
  for (const e of evidence) {
    const key = (e.sourceType || "agent_note") as string;
    if (!groups.has(key)) groups.set(key, []);
    groups.get(key)!.push(e);
  }
  return EVIDENCE_GROUP_ORDER.filter((k) => groups.has(k)).map((k) => ({ type: k, items: groups.get(k as string)! }));
}
