import { useEffect, useState } from "react";
import { Bug, Robot, Pencil, Plus, Trash } from "@phosphor-icons/react";
import type { AppActions } from "../../app/App";
import type {
  StockV2AgentExecutionDetail,
  StockV2AgentListResponse,
  StockV2AgentMCPStatus,
  StockV2AgentModelProfile,
  StockV2AgentProviderProfile,
  StockV2AgentRunCLIDebugRequest,
  StockV2AgentTaskProfile,
  StockV2AgentTaskType,
} from "../../app/types";
import { friendlyError } from "../../api/client";
import { Button, CollapsibleSection, Drawer, Field, Notice, Pill, useDangerConfirm } from "../../components/ui";
import {
  formatDate,
  stockV2AgentTaskConfigurable,
  stockV2AgentTaskTypeLabel,
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

type AgentTaskDefinition = {
  taskType: StockV2AgentTaskType;
  description: string;
};

const AGENT_TASKS: AgentTaskDefinition[] = [
  { taskType: "operation_review", description: "监控命中后的操作复核与建议生成。" },
  { taskType: "strategy_generation", description: "基于股票/组合上下文生成策略草案。" },
  { taskType: "opportunity_discovery", description: "从数据面与信息面筛选机会候选。" },
  { taskType: "news_event_review", description: "消息面事件关联股票并判断影响。" },
  { taskType: "portfolio_risk_review", description: "审查组合风险、暴露与约束冲突。" },
  { taskType: "stock_profile_summary", description: "生成股票画像与长期跟踪摘要。" },
  { taskType: "bull_bear_debate", description: "多空视角辩论与证据交叉检查。" },
];

export function StockV2AgentPage({ actions }: { actions: AppActions }) {
  const [providerDrawer, setProviderDrawer] = useState<ProviderDrawerState>({ type: "closed" });
  const [modelDrawer, setModelDrawer] = useState<ModelDrawerState>({ type: "closed" });
  const [taskProfileDrawer, setTaskProfileDrawer] = useState<TaskProfileDrawerState>({ type: "closed" });
  const [providers, setProviders] = useState<StockV2AgentProviderProfile[] | null>(null);
  const [models, setModels] = useState<StockV2AgentModelProfile[] | null>(null);
  const [taskProfiles, setTaskProfiles] = useState<StockV2AgentTaskProfile[] | null>(null);
  const [mcpStatus, setMCPStatus] = useState<StockV2AgentMCPStatus | null>(null);
  const [pLoading, setPLoading] = useState(false);
  const [mLoading, setMLoading] = useState(false);
  const [tLoading, setTLoading] = useState(false);
  const [mcpLoading, setMCPLoading] = useState(false);
  const [pError, setPError] = useState<string | null>(null);
  const [mError, setMError] = useState<string | null>(null);
  const [tError, setTError] = useState<string | null>(null);
  const [mcpError, setMCPError] = useState<string | null>(null);
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

  async function loadTaskProfiles(): Promise<StockV2AgentTaskProfile[]> {
    setTLoading(true);
    setTError(null);
    try {
      const res = await actions.api<StockV2AgentListResponse<StockV2AgentTaskProfile>>(
        "/api/stockv2/agent/task-profiles?limit=20",
      );
      const nextItems = res.items || [];
      setTaskProfiles(nextItems);
      return nextItems;
    } catch (err) {
      setTError(friendlyError(err));
      setTaskProfiles([]);
      return [];
    } finally {
      setTLoading(false);
    }
  }

  async function loadMCPStatus(): Promise<StockV2AgentMCPStatus | null> {
    setMCPLoading(true);
    setMCPError(null);
    try {
      const res = await actions.api<StockV2AgentMCPStatus>("/api/stockv2/agent/mcp/status");
      setMCPStatus(res);
      return res;
    } catch (err) {
      setMCPError(friendlyError(err));
      setMCPStatus(null);
      return null;
    } finally {
      setMCPLoading(false);
    }
  }

  useEffect(() => {
    void loadMCPStatus();
    void loadProviders();
    void loadModels();
    void loadTaskProfiles();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  async function ensureProvidersLoaded() {
    if (!providers) {
      await loadProviders();
    }
  }

  async function openCreateModelDrawer() {
    await ensureProvidersLoaded();
    setModelDrawer({ type: "create" });
  }

  async function openEditModelDrawer(model: StockV2AgentModelProfile) {
    await ensureProvidersLoaded();
    setModelDrawer({ type: "edit", model });
  }

  async function openCliDebugDrawer() {
    if (!models) {
      await loadModels();
    }
    setCliDebugOpen(true);
  }

  async function openTaskProfileDrawer(profile: StockV2AgentTaskProfile) {
    if (!models) {
      await loadModels();
    }
    setTaskProfileDrawer({ type: "edit", profile });
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
      body: `确定删除 "${name}" 吗？关联模型会同步删除，任务绑定会被清空。`,
      objectName: name,
      confirmLabel: "删除",
    });
    if (!ok) return;
    try {
      await actions.api(`/api/stockv2/agent/providers/${id}`, { method: "DELETE" });
      actions.setToast("已删除", "good");
      await loadProviders();
      await loadModels();
      await loadTaskProfiles();
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
        title="Codex MCP"
        subtitle="stock_agent.submit_result 本机回填通道"
        defaultOpen
      >
        <AgentMCPSection
          loading={mcpLoading || (!mcpStatus && !mcpError)}
          error={mcpError}
          status={mcpStatus}
          onRetry={loadMCPStatus}
        />
      </CollapsibleSection>

      <CollapsibleSection
        title="供应商 (Provider)"
        subtitle="Codex CLI 默认入口与外部 provider 可用性"
      >
        <AgentProviderSection
          loading={pLoading || providers === null}
          error={pError}
          items={providers}
          onRetry={loadProviders}
          onEdit={(p) => setProviderDrawer({ type: "edit", provider: p })}
          onDelete={(p) => void deleteProvider(p.id, p.name)}
        />
        <div className="mt-2 flex justify-end">
          <Button onClick={() => setProviderDrawer({ type: "create" })}>
            <Plus size={14} className="mr-1" /> 新建 Provider
          </Button>
        </div>
      </CollapsibleSection>

      <CollapsibleSection title="模型 (Model)" subtitle="按 provider 绑定的具体模型">
        <AgentModelSection
          loading={mLoading || models === null}
          error={mError}
          items={models}
          onRetry={loadModels}
          toggleBusy={toggleBusy}
          onToggle={toggleModelEnabled}
          onEdit={(m) => void openEditModelDrawer(m)}
        />
        <div className="mt-2 flex justify-end">
          <Button
            onClick={() => void openCreateModelDrawer()}
          >
            <Plus size={14} className="mr-1" /> 新建模型
          </Button>
        </div>
      </CollapsibleSection>

      <CollapsibleSection
        title="任务绑定"
        subtitle="已开放任务可绑定主备模型,未开放任务先置灰展示"
      >
        <AgentTaskProfileSection
          loading={tLoading || taskProfiles === null}
          error={tError}
          profiles={taskProfiles}
          onRetry={loadTaskProfiles}
          onEdit={(profile) => void openTaskProfileDrawer(profile)}
        />
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
          taskType={taskProfileDrawer.profile.taskType}
          taskLabel={stockV2AgentTaskTypeLabel(taskProfileDrawer.profile.taskType)}
          actions={actions}
          onClose={() => setTaskProfileDrawer({ type: "closed" })}
          onSaved={() => void loadTaskProfiles()}
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

function AgentMCPSection({
  loading,
  error,
  status,
  onRetry,
}: {
  loading: boolean;
  error: string | null;
  status: StockV2AgentMCPStatus | null;
  onRetry: () => Promise<StockV2AgentMCPStatus | null>;
}) {
  if (loading) return <p className="text-xs text-[var(--muted)]">加载中…</p>;
  if (error) {
    return (
      <div className="grid gap-2">
        <Notice tone="danger">{error}</Notice>
        <Button onClick={() => void onRetry()}>重试</Button>
      </div>
    );
  }
  if (!status) return <p className="text-xs text-[var(--muted)]">暂无 MCP 状态。</p>;
  return (
    <div className="grid gap-2">
      <div className="rounded border border-[var(--line)] bg-[var(--surface)] px-3 py-2 text-xs">
        <div className="flex flex-wrap items-center gap-2">
          <strong className="text-sm">{status.serverName || "stock_agent"}</strong>
          <Pill tone={status.enabled ? "good" : "warn"}>{status.enabled ? "可用" : "未启动"}</Pill>
          <Pill tone="neutral">{status.transport || "loopback_http"}</Pill>
        </div>
        <div className="mt-2 grid gap-1">
          <Row label="Endpoint" value={status.url || "(未启动)"} />
          <Row label="Tools" value={status.requiredTools?.join(", ") || "-"} />
        </div>
      </div>
      <div className="flex justify-end">
        <Button onClick={() => void onRetry()}>刷新</Button>
      </div>
    </div>
  );
}

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
                    <Pencil size={12} className="mr-1" /> 配置
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
              <Pencil size={12} className="mr-1" /> 配置
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
  profiles,
  onRetry,
  onEdit,
}: {
  loading: boolean;
  error: string | null;
  profiles: StockV2AgentTaskProfile[] | null;
  onRetry: () => Promise<StockV2AgentTaskProfile[]>;
  onEdit: (profile: StockV2AgentTaskProfile) => void;
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
  if (!profiles || profiles.length === 0) return <p className="text-xs text-[var(--muted)]">暂无任务配置。</p>;
  const profileByType = new Map(profiles.map((item) => [item.taskType, item]));
  return (
    <div className="grid gap-2">
      {AGENT_TASKS.map((task) => {
        const profile = profileByType.get(task.taskType);
        const configurable = stockV2AgentTaskConfigurable(task.taskType);
        return (
          <div
            key={task.taskType}
            className={`rounded border border-[var(--line)] bg-[var(--surface)] px-3 py-2 text-xs ${
              configurable ? "" : "opacity-60"
            }`}
          >
            <div className="flex flex-wrap items-center justify-between gap-2">
              <div className="min-w-0">
                <div className="flex flex-wrap items-center gap-2">
                  <strong className="text-sm">{stockV2AgentTaskTypeLabel(task.taskType)}</strong>
                  <Pill tone={configurable ? "good" : "neutral"}>{configurable ? "已开放" : "未开放"}</Pill>
                  {profile?.maxBudget ? <Pill tone="neutral">预算 {profile.maxBudget}</Pill> : null}
                </div>
                <p className="mt-1 text-[var(--muted)]">{task.description}</p>
              </div>
              {configurable && profile ? (
                <Button onClick={() => onEdit(profile)}>
                  <Pencil size={12} className="mr-1" /> 编辑绑定
                </Button>
              ) : null}
            </div>
            {configurable ? (
              <div className="mt-2 grid gap-1">
                <Row label="主模型" value={profile?.primaryModelId ? profile.primaryModelId.slice(0, 12) : "(未绑定)"} />
                <Row label="备模型" value={profile?.fallbackModelId ? profile.fallbackModelId.slice(0, 12) : "(未绑定)"} />
              </div>
            ) : (
              <p className="mt-2 text-[var(--muted)]">暂不允许选择模型,后续开放时再配置绑定。</p>
            )}
          </div>
        );
      })}
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
  const usableModels = models.filter((model) => model.enabled && model.status === "available");
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
    const first = nextItems.find((model) => model.enabled && model.status === "available")?.id || nextItems[0]?.id || "";
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
                {model.displayName || model.modelName}{model.enabled && model.status === "available" ? "" : " (不可用于任务)"}
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
