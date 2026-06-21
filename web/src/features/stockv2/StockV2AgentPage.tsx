import { useState } from "react";
import { Bug, Robot, Pencil, Plus, Trash } from "@phosphor-icons/react";
import type { AppActions } from "../../app/App";
import type {
  StockV2AgentExecutionDetail,
  StockV2AgentListResponse,
  StockV2AgentModelProfile,
  StockV2AgentProviderProfile,
  StockV2AgentRunCLIDebugRequest,
  StockV2AgentTaskProfile,
} from "../../app/types";
import { friendlyError } from "../../api/client";
import { Button, CollapsibleSection, Drawer, Field, Notice, Pill, useDangerConfirm } from "../../components/ui";
import {
  formatDate,
  stockV2AgentProviderTypeLabel,
} from "../../domain/labels";
import { StockV2AgentRunDetailPanel } from "./StockV2AgentExecutionLedger";
import { StockV2AgentProviderDrawer } from "./StockV2AgentProviderDrawer";
import { StockV2AgentModelDrawer } from "./StockV2AgentModelDrawer";
import { StockV2AgentTaskProfileDrawer } from "./StockV2AgentTaskProfileDrawer";

// Agent 治理独立页。Quiet 风格 + 折叠区 + Drawer 渐进披露。
// Provider/Model/TaskProfile 支持配置；运行留痕在“行情与监控”的 Agent 执行台账查看。

type ProviderDrawerState =
  | { type: "closed" }
  | { type: "create" }
  | { type: "edit"; provider: StockV2AgentProviderProfile };

type ModelDrawerState =
  | { type: "closed" }
  | { type: "create" }
  | { type: "edit"; model: StockV2AgentModelProfile };

type TaskProfileDrawerState =
  | { type: "closed" }
  | { type: "edit"; profile: StockV2AgentTaskProfile };

export function StockV2AgentPage({ actions }: { actions: AppActions }) {
  const [providerDrawer, setProviderDrawer] = useState<ProviderDrawerState>({ type: "closed" });
  const [modelDrawer, setModelDrawer] = useState<ModelDrawerState>({ type: "closed" });
  const [taskProfileDrawer, setTaskProfileDrawer] = useState<TaskProfileDrawerState>({ type: "closed" });
  const [providers, setProviders] = useState<StockV2AgentProviderProfile[] | null>(null);
  const [models, setModels] = useState<StockV2AgentModelProfile[] | null>(null);
  const [taskProfile, setTaskProfile] = useState<StockV2AgentTaskProfile | null>(null);
  const [pLoading, setPLoading] = useState(false);
  const [mLoading, setMLoading] = useState(false);
  const [tLoading, setTLoading] = useState(false);
  const [pError, setPError] = useState<string | null>(null);
  const [mError, setMError] = useState<string | null>(null);
  const [tError, setTError] = useState<string | null>(null);
  const [pStarted, setPStarted] = useState(false);
  const [mStarted, setMStarted] = useState(false);
  const [tStarted, setTStarted] = useState(false);
  const [toggleBusy, setToggleBusy] = useState<string | null>(null);
  const [cliDebugOpen, setCliDebugOpen] = useState(false);
  const { confirmDanger, dangerConfirmDialog } = useDangerConfirm();

  async function loadProviders(): Promise<StockV2AgentProviderProfile[]> {
    setPLoading(true);
    setPError(null);
    try {
      const res = await actions.api<StockV2AgentListResponse<StockV2AgentProviderProfile>>(
        "/api/stockv2/agent/providers",
      );
      const nextItems = res.items || [];
      setProviders(nextItems);
      return nextItems;
    } catch (err) {
      setPError(friendlyError(err));
      setProviders([]);
      return [];
    } finally {
      setPLoading(false);
    }
  }

  async function loadModels(): Promise<StockV2AgentModelProfile[]> {
    setMLoading(true);
    setMError(null);
    try {
      const res = await actions.api<StockV2AgentListResponse<StockV2AgentModelProfile>>(
        "/api/stockv2/agent/models",
      );
      const nextItems = res.items || [];
      setModels(nextItems);
      return nextItems;
    } catch (err) {
      setMError(friendlyError(err));
      setModels([]);
      return [];
    } finally {
      setMLoading(false);
    }
  }

  async function loadTaskProfile() {
    setTLoading(true);
    setTError(null);
    try {
      const res = await actions.api<StockV2AgentTaskProfile>(
        "/api/stockv2/agent/task-profiles/operation_review",
      );
      setTaskProfile(res);
    } catch (err) {
      setTError(friendlyError(err));
      setTaskProfile(null);
    } finally {
      setTLoading(false);
    }
  }

  function openProviderSection() {
    if (!pStarted) {
      setPStarted(true);
      void loadProviders();
    }
  }
  function openModelSection() {
    if (!mStarted) {
      setMStarted(true);
      void loadModels();
    }
  }
  function openTaskSection() {
    if (!tStarted) {
      setTStarted(true);
      void loadTaskProfile();
    }
  }

  async function ensureProvidersLoaded() {
    if (!pStarted) {
      setPStarted(true);
    }
    if (!providers) {
      await loadProviders();
    }
  }

  async function openCreateModelDrawer() {
    openModelSection();
    await ensureProvidersLoaded();
    setModelDrawer({ type: "create" });
  }

  async function openEditModelDrawer(model: StockV2AgentModelProfile) {
    await ensureProvidersLoaded();
    setModelDrawer({ type: "edit", model });
  }

  async function openCliDebugDrawer() {
    openModelSection();
    if (!models) {
      await loadModels();
    }
    setCliDebugOpen(true);
  }

  async function toggleModelEnabled(id: string, enabled: boolean) {
    setToggleBusy(id);
    try {
      await actions.api(`/api/stockv2/agent/models/${id}`, {
        method: "PUT",
        body: { enabled },
      });
      actions.setToast(enabled ? "已启用" : "已禁用", "good");
      await loadModels();
    } catch (err) {
      actions.setToast(friendlyError(err), "danger");
    } finally {
      setToggleBusy(null);
    }
  }

  async function deleteProvider(id: string, name: string) {
    const ok = await confirmDanger({
      title: "删除 Provider",
      body: `确定删除 "${name}" 吗？关联的模型配置不会自动清理。`,
      objectName: name,
      confirmLabel: "删除",
    });
    if (!ok) return;
    try {
      await actions.api(`/api/stockv2/agent/providers/${id}`, { method: "DELETE" });
      actions.setToast("已删除", "good");
      await loadProviders();
    } catch (err) {
      actions.setToast(friendlyError(err), "danger");
    }
  }

  return (
    <div className="grid gap-3">
      <div className="flex items-start justify-between gap-3">
        <div>
          <div className="flex items-center gap-2">
            <Robot size={18} />
            <h2 className="text-lg font-semibold">Agent 治理</h2>
          </div>
          <p className="mt-1 text-xs text-[var(--muted)]">
            供应商 / 模型 / 任务绑定。默认 Provider 使用本机 Codex CLI，外部 Provider 使用 OpenAI-compatible 配置。
          </p>
        </div>
        <Button onClick={() => void openCliDebugDrawer()} title="验证 Codex CLI 与 MCP 回填链路">
          <Bug size={14} className="mr-1" />
          验证 CLI
        </Button>
      </div>

      <CollapsibleSection
        title="供应商 (Provider)"
        subtitle="Codex CLI 默认入口与外部 provider 可用性"
      >
        <div onClick={openProviderSection}>
          <AgentProviderSection
            loading={!pStarted || pLoading}
            error={pError}
            items={providers}
            onRetry={loadProviders}
            onEdit={(p) => setProviderDrawer({ type: "edit", provider: p })}
            onDelete={(p) => void deleteProvider(p.id, p.name)}
          />
        </div>
        <div className="mt-2 flex justify-end">
          <Button onClick={() => setProviderDrawer({ type: "create" })}>
            <Plus size={14} className="mr-1" /> 新建 Provider
          </Button>
        </div>
      </CollapsibleSection>

      <CollapsibleSection title="模型 (Model)" subtitle="按 provider 绑定的具体模型">
        <div onClick={openModelSection}>
          <AgentModelSection
            loading={!mStarted || mLoading}
            error={mError}
            items={models}
            onRetry={loadModels}
            toggleBusy={toggleBusy}
            onToggle={toggleModelEnabled}
            onEdit={(m) => void openEditModelDrawer(m)}
          />
        </div>
        <div className="mt-2 flex justify-end">
          <Button
            onClick={() => void openCreateModelDrawer()}
          >
            <Plus size={14} className="mr-1" /> 新建模型
          </Button>
        </div>
      </CollapsibleSection>

      <CollapsibleSection
        title="任务绑定 · operation_review"
        subtitle="任务到主备模型的绑定"
      >
        <div onClick={openTaskSection}>
          <AgentTaskProfileSection
            loading={!tStarted || tLoading}
            error={tError}
            profile={taskProfile}
            onRetry={loadTaskProfile}
          />
        </div>
        <div className="mt-2 flex justify-end">
          <Button
            disabled={!taskProfile}
            onClick={() => {
              if (taskProfile) setTaskProfileDrawer({ type: "edit", profile: taskProfile });
            }}
          >
            <Pencil size={14} className="mr-1" /> 编辑绑定
          </Button>
        </div>
      </CollapsibleSection>

      {providerDrawer.type !== "closed" ? (
        <StockV2AgentProviderDrawer
          provider={providerDrawer.type === "edit" ? providerDrawer.provider : null}
          actions={actions}
          onClose={() => setProviderDrawer({ type: "closed" })}
          onSaved={() => void loadProviders()}
        />
      ) : null}

      {modelDrawer.type !== "closed" ? (
        <StockV2AgentModelDrawer
          model={modelDrawer.type === "edit" ? modelDrawer.model : null}
          providers={providers ?? []}
          actions={actions}
          onClose={() => setModelDrawer({ type: "closed" })}
          onSaved={() => void loadModels()}
        />
      ) : null}

      {taskProfileDrawer.type !== "closed" ? (
        <StockV2AgentTaskProfileDrawer
          profile={taskProfileDrawer.profile}
          models={models ?? []}
          taskType="operation_review"
          actions={actions}
          onClose={() => setTaskProfileDrawer({ type: "closed" })}
          onSaved={() => void loadTaskProfile()}
        />
      ) : null}

      {cliDebugOpen ? (
        <AgentCLIDebugDrawer
          actions={actions}
          models={models ?? []}
          onClose={() => setCliDebugOpen(false)}
          onReloadModels={loadModels}
        />
      ) : null}

      {dangerConfirmDialog}
    </div>
  );
}

// ============ Section 子组件 ============

function AgentProviderSection({
  loading,
  error,
  items,
  onRetry,
  onEdit,
  onDelete,
}: {
  loading: boolean;
  error: string | null;
  items: StockV2AgentProviderProfile[] | null;
  onRetry: () => void;
  onEdit: (p: StockV2AgentProviderProfile) => void;
  onDelete: (p: StockV2AgentProviderProfile) => void;
}) {
  if (loading) return <p className="text-xs text-[var(--muted)]">加载中…</p>;
  if (error) {
    return (
      <div className="grid gap-2">
        <Notice tone="danger">{error}</Notice>
        <Button onClick={onRetry}>重试</Button>
      </div>
    );
  }
  if (!items || items.length === 0) return <p className="text-xs text-[var(--muted)]">暂无 Provider。</p>;
  return (
    <div className="grid gap-2">
      {items.map((p) => {
        const isDefaultProvider = isDefaultCodexCLIProvider(p);
        return (
          <div key={p.id} className="rounded border border-[var(--line)] bg-[var(--surface)] px-3 py-2 text-xs">
            <div className="flex flex-wrap items-center gap-2">
              <strong className="text-sm">{p.displayName || p.name}</strong>
              <span className="font-mono text-[var(--muted-strong)]">{p.name}</span>
              <Pill tone="neutral">{stockV2AgentProviderTypeLabel(p.providerType)}</Pill>
              {isDefaultProvider ? (
                <Pill tone="good">本机 CLI</Pill>
              ) : (
                <Pill tone={p.apiKeySet ? "good" : "warn"}>{p.apiKeySet ? "Token 已设置" : "Token 未设置"}</Pill>
              )}
            </div>
            <p className="mt-1 break-words font-mono text-[var(--muted)]">
              {isDefaultProvider ? "codex login session on this host" : p.baseUrl || "-"}
            </p>
            {p.lastProbeResult ? (
              <p className="mt-1 break-words text-[var(--muted)]">
                探测 {formatDate(p.lastProbeAt) || "-"}: {p.lastProbeResult}
              </p>
            ) : null}
            <div className="mt-1.5 flex justify-end gap-1.5">
              {isDefaultProvider ? (
                <Pill tone="neutral">系统内置</Pill>
              ) : (
                <>
                  <Button onClick={() => onEdit(p)}>
                    <Pencil size={12} className="mr-1" /> 编辑
                  </Button>
                  <Button tone="danger" onClick={() => onDelete(p)}>
                    <Trash size={12} className="mr-1" /> 删除
                  </Button>
                </>
              )}
            </div>
          </div>
        );
      })}
    </div>
  );
}

function isDefaultCodexCLIProvider(provider: StockV2AgentProviderProfile): boolean {
  return provider.id === "agent-provider-codex-cli-default" && provider.providerType === "codex_cli";
}

function AgentModelSection({
  loading,
  error,
  items,
  onRetry,
  toggleBusy,
  onToggle,
  onEdit,
}: {
  loading: boolean;
  error: string | null;
  items: StockV2AgentModelProfile[] | null;
  onRetry: () => void;
  toggleBusy: string | null;
  onToggle: (id: string, enabled: boolean) => void;
  onEdit: (m: StockV2AgentModelProfile) => void;
}) {
  if (loading) return <p className="text-xs text-[var(--muted)]">加载中…</p>;
  if (error) {
    return (
      <div className="grid gap-2">
        <Notice tone="danger">{error}</Notice>
        <Button onClick={onRetry}>重试</Button>
      </div>
    );
  }
  if (!items || items.length === 0) return <p className="text-xs text-[var(--muted)]">暂无模型。</p>;
  return (
    <div className="grid gap-2">
      {items.map((m) => (
        <div key={m.id} className="rounded border border-[var(--line)] bg-[var(--surface)] px-3 py-2 text-xs">
          <div className="flex flex-wrap items-center gap-2">
            <strong className="text-sm">{m.displayName || m.modelName}</strong>
            <span className="font-mono text-[var(--muted-strong)]">{m.modelName}</span>
            <button
              type="button"
              disabled={toggleBusy === m.id}
              onClick={(e) => {
                e.stopPropagation();
                void onToggle(m.id, !m.enabled);
              }}
              className={`cursor-pointer rounded border px-2 py-0.5 text-[11px] ${
                m.enabled
                  ? "border-[rgba(18,132,79,0.3)] bg-[var(--good-soft)] text-[var(--good)]"
                  : "border-[var(--line)] bg-[var(--surface)] text-[var(--muted)]"
              }`}
            >
              {m.enabled ? "已启用" : "未启用"}
            </button>
          </div>
          <div className="mt-1 text-[var(--muted)]">
            provider {m.providerId.slice(0, 8)}
            {m.contextLimit ? ` · 上下文 ${m.contextLimit}` : ""}
          </div>
          <div className="mt-1.5 flex justify-end">
            <Button onClick={() => onEdit(m)}>
              <Pencil size={12} className="mr-1" /> 编辑
            </Button>
          </div>
        </div>
      ))}
    </div>
  );
}

function AgentTaskProfileSection({
  loading,
  error,
  profile,
  onRetry,
}: {
  loading: boolean;
  error: string | null;
  profile: StockV2AgentTaskProfile | null;
  onRetry: () => void;
}) {
  if (loading) return <p className="text-xs text-[var(--muted)]">加载中…</p>;
  if (error) {
    return (
      <div className="grid gap-2">
        <Notice tone="danger">{error}</Notice>
        <Button onClick={onRetry}>重试</Button>
      </div>
    );
  }
  if (!profile) return <p className="text-xs text-[var(--muted)]">暂无任务配置。</p>;
  return (
    <div className="grid gap-1.5 rounded border border-[var(--line)] bg-[var(--surface)] px-3 py-2 text-xs">
      <div className="flex flex-wrap items-center gap-2">
        <strong className="text-sm">operation_review</strong>
        {profile.maxBudget ? <Pill tone="neutral">预算 {profile.maxBudget}</Pill> : null}
      </div>
      <Row label="主模型" value={profile.primaryModelId ? profile.primaryModelId.slice(0, 12) : "(未绑定)"} />
      <Row label="备模型" value={profile.fallbackModelId ? profile.fallbackModelId.slice(0, 12) : "(未绑定)"} />
    </div>
  );
}

function AgentCLIDebugDrawer({
  actions,
  models,
  onClose,
  onReloadModels,
}: {
  actions: AppActions;
  models: StockV2AgentModelProfile[];
  onClose: () => void;
  onReloadModels: () => Promise<StockV2AgentModelProfile[]>;
}) {
  const usableModels = models.filter((model) => model.enabled);
  const [modelId, setModelId] = useState(usableModels[0]?.id || models[0]?.id || "");
  const [submitting, setSubmitting] = useState(false);
  const [result, setResult] = useState<StockV2AgentExecutionDetail | null>(null);
  const [error, setError] = useState<string | null>(null);

  async function runDebug() {
    if (!modelId) return;
    setSubmitting(true);
    setError(null);
    setResult(null);
    try {
      const body: StockV2AgentRunCLIDebugRequest = { modelId };
      const res = await actions.api<StockV2AgentExecutionDetail>("/api/stockv2/agent/cli-debug", {
        method: "POST",
        body,
      });
      setResult(res);
      actions.setToast(res.run.status === "completed" ? "CLI 验证完成" : "CLI 验证已返回失败上下文", res.run.status === "completed" ? "good" : "warn");
    } catch (err) {
      setError(friendlyError(err));
      actions.setToast(friendlyError(err), "danger");
    } finally {
      setSubmitting(false);
    }
  }

  async function reloadModels() {
    const nextItems = await onReloadModels();
    const first = nextItems.find((model) => model.enabled)?.id || nextItems[0]?.id || "";
    setModelId((current) => current || first);
  }

  return (
    <Drawer title="验证 CLI" subtitle="运行一次 Codex CLI + MCP submit_result 调试任务" onClose={onClose} width={720}>
      <div className="grid gap-4">
        <Field label="模型">
          <select value={modelId} onChange={(event) => setModelId(event.target.value)}>
            {models.length === 0 ? <option value="">暂无模型</option> : null}
            {models.map((model) => (
              <option key={model.id} value={model.id}>
                {model.displayName || model.modelName}{model.enabled ? "" : " (未启用)"}
              </option>
            ))}
          </select>
        </Field>
        <p className="text-xs leading-relaxed text-[var(--muted)]">
          这会启动一次真实 Codex CLI 调试任务,并要求 CLI 通过股票模块 MCP 回填结果。完成后下方会显示 stdout/stderr 摘要和 MCP 结构化结果。
        </p>
        {error ? <Notice tone="danger">{error}</Notice> : null}
        <div className="flex justify-end gap-2 border-t border-[var(--line)] pt-3">
          <Button onClick={() => void reloadModels()} disabled={submitting}>刷新模型</Button>
          <Button onClick={onClose} disabled={submitting}>关闭</Button>
          <Button tone="primary" disabled={submitting || !modelId} onClick={() => void runDebug()}>
            {submitting ? "调试中…" : "开始调试"}
          </Button>
        </div>
        {result ? <StockV2AgentRunDetailPanel detail={result} /> : null}
      </div>
    </Drawer>
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
