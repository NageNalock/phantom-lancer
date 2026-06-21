import { useEffect, useState } from "react";
import type { AppActions } from "../../app/App";
import type {
  StockV2AgentDecisionLedger,
  StockV2AgentListResponse,
  StockV2AgentModelProfile,
  StockV2AgentProviderProfile,
  StockV2AgentRun,
  StockV2AgentTaskProfile,
} from "../../app/types";
import { friendlyError } from "../../api/client";
import { Button, CollapsibleSection, Notice, Pill } from "../../components/ui";
import {
  formatDate,
  stockV2AgentAuthStateLabel,
  stockV2AgentAvailabilityLabel,
  stockV2AgentAvailabilityTone,
  stockV2AgentConfigStateLabel,
  stockV2AgentModelCostLevelLabel,
  stockV2AgentModelStatusLabel,
  stockV2AgentModelStatusTone,
  stockV2AgentProviderTypeLabel,
  stockV2AgentRunStatusLabel,
  stockV2AgentRunStatusTone,
} from "../../domain/labels";

// Agent 治理轻量入口(Quiet 风格):若干折叠区,按需懒加载。只读观测为主,
// 不建 provider/model/task-profile 编辑表单,不真实调用模型。复杂信息(ledger 脱敏原文)进折叠。

export function StockV2AgentConsole({ actions }: { actions: AppActions }) {
  return (
    <div className="grid gap-3">
      <p className="text-xs text-[var(--muted)]">
        供应商 / 模型 / 任务绑定 / 运行与决策留痕的只读观测。展开各区块按需加载。
      </p>
      <CollapsibleSection title="供应商 (Provider)" subtitle="openai / codex_cli / local 配置与可用性">
        <AgentProviderSection actions={actions} />
      </CollapsibleSection>
      <CollapsibleSection title="模型 (Model)" subtitle="按 provider 绑定的具体模型">
        <AgentModelSection actions={actions} />
      </CollapsibleSection>
      <CollapsibleSection title="任务绑定 · operation_review" subtitle="任务到主备模型的绑定">
        <AgentTaskProfileSection actions={actions} />
      </CollapsibleSection>
      <CollapsibleSection title="最近运行与决策留痕" subtitle="AgentRun + DecisionLedger">
        <AgentRunSection actions={actions} />
      </CollapsibleSection>
    </div>
  );
}

// ===== 通用 section 外壳:loading / error / empty =====

function SectionState({
  loading,
  error,
  empty,
  onRetry,
  children,
}: {
  loading: boolean;
  error: string | null;
  empty: boolean;
  onRetry?: () => void;
  children: React.ReactNode;
}) {
  if (loading) return <p className="text-xs text-[var(--muted)]">加载中…</p>;
  if (error) {
    return (
      <div className="grid gap-2">
        <Notice tone="danger">{error}</Notice>
        {onRetry ? <Button onClick={onRetry}>重试</Button> : null}
      </div>
    );
  }
  if (empty) return <p className="text-xs text-[var(--muted)]">暂无记录。</p>;
  return <>{children}</>;
}

function AgentProviderSection({ actions }: { actions: AppActions }) {
  const [items, setItems] = useState<StockV2AgentProviderProfile[] | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function load() {
    setLoading(true);
    setError(null);
    try {
      const res = await actions.api<StockV2AgentListResponse<StockV2AgentProviderProfile>>("/api/stockv2/agent/providers");
      setItems(res.items || []);
    } catch (err) {
      setError(friendlyError(err));
      setItems([]);
    } finally {
      setLoading(false);
    }
  }

  return (
    <SectionLoader loading={loading} error={error} items={items} onRetry={load} onLoad={load}>
      <div className="grid gap-2">
        {(items ?? []).map((p) => (
          <div key={p.id} className="rounded border border-[var(--line)] bg-[var(--surface)] px-3 py-2 text-xs">
            <div className="flex flex-wrap items-center gap-2">
              <strong className="text-sm">{p.name}</strong>
              <Pill tone="neutral">{stockV2AgentProviderTypeLabel(p.providerType)}</Pill>
              <Pill tone="neutral">{stockV2AgentConfigStateLabel(p.configState)}</Pill>
              <Pill tone="neutral">{stockV2AgentAuthStateLabel(p.authState)}</Pill>
              <Pill tone={stockV2AgentAvailabilityTone(p.availability)}>
                {stockV2AgentAvailabilityLabel(p.availability)}
              </Pill>
            </div>
            {p.lastProbeResult ? (
              <p className="mt-1 break-words text-[var(--muted)]">探测 {formatDate(p.lastProbeAt) || "-"}: {p.lastProbeResult}</p>
            ) : null}
          </div>
        ))}
      </div>
    </SectionLoader>
  );
}

function AgentModelSection({ actions }: { actions: AppActions }) {
  const [items, setItems] = useState<StockV2AgentModelProfile[] | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function load() {
    setLoading(true);
    setError(null);
    try {
      const res = await actions.api<StockV2AgentListResponse<StockV2AgentModelProfile>>("/api/stockv2/agent/models");
      setItems(res.items || []);
    } catch (err) {
      setError(friendlyError(err));
      setItems([]);
    } finally {
      setLoading(false);
    }
  }

  return (
    <SectionLoader loading={loading} error={error} items={items} onRetry={load} onLoad={load}>
      <div className="grid gap-2">
        {(items ?? []).map((m) => (
          <div key={m.id} className="rounded border border-[var(--line)] bg-[var(--surface)] px-3 py-2 text-xs">
            <div className="flex flex-wrap items-center gap-2">
              <strong className="text-sm">{m.displayName || m.modelName}</strong>
              <span className="font-mono text-[var(--muted-strong)]">{m.modelName}</span>
              {m.enabled ? <Pill tone="good">已启用</Pill> : <Pill tone="neutral">未启用</Pill>}
              <Pill tone={stockV2AgentModelStatusTone(m.status)}>{stockV2AgentModelStatusLabel(m.status)}</Pill>
              <Pill tone="neutral">{stockV2AgentModelCostLevelLabel(m.costLevel)}</Pill>
            </div>
            <div className="mt-1 text-[var(--muted)]">provider {m.providerId.slice(0, 8)}{m.contextLimit ? ` · 上下文 ${m.contextLimit}` : ""}</div>
          </div>
        ))}
      </div>
    </SectionLoader>
  );
}

function AgentTaskProfileSection({ actions }: { actions: AppActions }) {
  const [profile, setProfile] = useState<StockV2AgentTaskProfile | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function load() {
    setLoading(true);
    setError(null);
    try {
      const res = await actions.api<StockV2AgentTaskProfile>(
        "/api/stockv2/agent/task-profiles/operation_review",
      );
      setProfile(res);
    } catch (err) {
      setError(friendlyError(err));
      setProfile(null);
    } finally {
      setLoading(false);
    }
  }

  return (
    <SectionLoader loading={loading} error={error} items={profile ? [profile] : null} onRetry={load} onLoad={load}>
      <div className="grid gap-1.5 rounded border border-[var(--line)] bg-[var(--surface)] px-3 py-2 text-xs">
        <div className="flex flex-wrap items-center gap-2">
          <strong className="text-sm">operation_review</strong>
          {profile?.maxBudget ? <Pill tone="neutral">预算 {profile.maxBudget}</Pill> : null}
        </div>
        <Row label="主模型" value={profile?.primaryModelId ? profile.primaryModelId.slice(0, 12) : "(未绑定)"} />
        <Row label="备模型" value={profile?.fallbackModelId ? profile.fallbackModelId.slice(0, 12) : "(未绑定)"} />
        <p className="mt-1 text-[var(--muted)]">任务 profile 由后端默认种入,模型绑定在 Agent 治理中维护。</p>
      </div>
    </SectionLoader>
  );
}

function AgentRunSection({ actions }: { actions: AppActions }) {
  const [items, setItems] = useState<StockV2AgentRun[] | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [openLedger, setOpenLedger] = useState<string | null>(null);
  const [ledger, setLedger] = useState<StockV2AgentDecisionLedger | null>(null);
  const [ledgerError, setLedgerError] = useState<string | null>(null);

  async function load() {
    setLoading(true);
    setError(null);
    try {
      const res = await actions.api<StockV2AgentListResponse<StockV2AgentRun>>("/api/stockv2/agent/runs?limit=10");
      setItems(res.items || []);
    } catch (err) {
      setError(friendlyError(err));
      setItems([]);
    } finally {
      setLoading(false);
    }
  }

  async function toggleLedger(run: StockV2AgentRun) {
    if (!run.decisionLedgerId) return;
    if (openLedger === run.id) {
      setOpenLedger(null);
      setLedger(null);
      return;
    }
    setOpenLedger(run.id);
    setLedger(null);
    setLedgerError(null);
    try {
      const res = await actions.api<StockV2AgentDecisionLedger>(
        `/api/stockv2/agent/ledgers/${run.decisionLedgerId}`,
      );
      setLedger(res);
    } catch (err) {
      setLedgerError(friendlyError(err));
    }
  }

  return (
    <SectionLoader loading={loading} error={error} items={items} onRetry={load} onLoad={load}>
      <div className="grid gap-2">
        {(items ?? []).map((run) => (
          <div key={run.id} className="rounded border border-[var(--line)] bg-[var(--surface)] px-3 py-2 text-xs">
            <div className="flex flex-wrap items-center gap-2">
              <strong className="text-sm">{run.taskType}</strong>
              <Pill tone={stockV2AgentRunStatusTone(run.status)}>{stockV2AgentRunStatusLabel(run.status)}</Pill>
              <span className="font-mono text-[var(--muted-strong)]">model {run.modelId?.slice(0, 8) || "-"}</span>
              <span className="text-[var(--muted)]">{formatDate(run.startedAt || run.createdAt) || "-"}</span>
              {run.triggerObjectType ? (
                <span className="text-[var(--muted)]">{run.triggerObjectType}:{run.triggerObjectId?.slice(0, 8) || "-"}</span>
              ) : null}
            </div>
            {run.errorMessage ? <p className="mt-1 break-words text-[var(--danger)]">{run.errorMessage}</p> : null}
            {run.decisionLedgerId ? (
              <div className="mt-1.5">
                <button
                  type="button"
                  className="text-[var(--accent)] hover:underline"
                  onClick={() => void toggleLedger(run)}
                >
                  {openLedger === run.id ? "收起决策留痕" : "查看决策留痕"}
                </button>
                {openLedger === run.id ? (
                  <LedgerDetail ledger={ledger} error={ledgerError} />
                ) : null}
              </div>
            ) : null}
          </div>
        ))}
      </div>
    </SectionLoader>
  );
}

function LedgerDetail({ ledger, error }: { ledger: StockV2AgentDecisionLedger | null; error: string | null }) {
  if (error) return <p className="mt-1 text-[var(--danger)]">{error}</p>;
  if (!ledger) return <p className="mt-1 text-[var(--muted)]">加载留痕…</p>;
  return (
    <div className="mt-1.5 grid gap-1.5 rounded border border-[var(--line)] bg-[var(--surface-soft)] px-2 py-2">
      <Row label="输入摘要" value={ledger.inputSummary || "(空)"} />
      <Row label="Prompt" value={ledger.prompt || "(空)"} />
      {ledger.outputArtifactSummary ? <Row label="输出摘要" value={ledger.outputArtifactSummary} /> : null}
      <details className="rounded border border-[var(--line)] bg-[var(--surface)]">
        <summary className="cursor-pointer px-2 py-1 text-[var(--muted)]">脱敏留痕 / 结构化输出</summary>
        <pre className="max-h-40 overflow-auto px-2 py-2 text-[11px] text-[var(--muted-strong)]">
          {stringify({ redaction: ledger.redactionSummary, structuredOutput: ledger.structuredOutput })}
        </pre>
      </details>
    </div>
  );
}

// ===== 小工具 =====

function Row({ label, value }: { label: string; value: string }) {
  return (
    <div className="grid grid-cols-[72px_minmax(0,1fr)] gap-2">
      <span className="text-[var(--muted)]">{label}</span>
      <span className="break-words text-[var(--muted-strong)]">{value}</span>
    </div>
  );
}

function stringify(value: unknown): string {
  try {
    return JSON.stringify(value, null, 2);
  } catch {
    return String(value);
  }
}

// SectionLoader:首次展开(onLoad)拉取一次,之后用 items 是否为 null 区分"未加载/空"。
function SectionLoader<T>({
  loading,
  error,
  items,
  onRetry,
  onLoad,
  children,
}: {
  loading: boolean;
  error: string | null;
  items: T[] | null;
  onRetry?: () => void;
  onLoad: () => void;
  children: React.ReactNode;
}) {
  const [started, setStarted] = useState(false);
  useEffect(() => {
    if (!started) {
      setStarted(true);
      onLoad();
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [started]);
  return (
    <SectionState
      loading={loading || !started}
      error={error}
      empty={started && !loading && !error && (items?.length ?? 0) === 0}
      onRetry={onRetry}
    >
      {children}
    </SectionState>
  );
}
