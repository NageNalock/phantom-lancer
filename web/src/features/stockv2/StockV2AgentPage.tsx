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
  stockV2AgentAvailabilityLabel,
  stockV2AgentAvailabilityTone,
  stockV2AgentModelStatusLabel,
  stockV2AgentModelStatusTone,
  stockV2AgentTaskConfigurable,
  stockV2AgentTaskTypeLabel,
  stockV2AgentProviderTypeLabel,
  stockV2AgentRunStatusLabel,
  stockV2AgentRunStatusTone,
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
  | { type: "create"; modelType: "chat" | "embedding" }
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
  { taskType: "portfolio_sentinel", description: "周期复核组合风险，并消费消息脉络产生的实质变化。" },
  { taskType: "news_event_review", description: "定期归纳新闻、持续完善消息脉络主题并触发影响复核。" },
  { taskType: "stock_profile_summary", description: "生成股票画像与长期跟踪摘要。" },
  { taskType: "bull_bear_debate", description: "多空视角辩论与证据交叉检查。" },
];

type MCPToolInfo = {
  name: string;
  group: string;
  purpose: string;
};

const MCP_TOOL_INFO: MCPToolInfo[] = [
  { name: "stock_agent.search_instruments", group: "主数据查询", purpose: "按关键词、市场和类型检索 StockV2 标的主数据，用于确认候选 symbol 是否存在。" },
  { name: "stock_agent.search_stock_profiles", group: "项目资料查询", purpose: "关键词检索股票画像，给 Agent 提供公司业务、产业链和长期跟踪摘要。" },
  { name: "stock_agent.semantic_search_stock_profiles", group: "语义召回", purpose: "基于向量资产语义检索股票画像；未绑定可用嵌入模型或资产未就绪时会直接失败。" },
  { name: "stock_agent.get_stock_profile", group: "项目资料查询", purpose: "读取单只股票的完整画像，用于核对候选关系、风险和已有上下文。" },
  { name: "stock_agent.get_latest_quotes", group: "行情查询", purpose: "读取最新行情摘要，帮助 Agent 判断候选的当前交易状态和数据新鲜度。" },
  { name: "stock_agent.get_daily_bars_summary", group: "行情查询", purpose: "读取日 K 摘要，给市场风险、趋势和波动判断提供基础数据。" },
  { name: "stock_agent.search_news_events", group: "项目资料查询", purpose: "关键词检索本地新闻事件库，用于把主题和已有消息面记录关联起来。" },
  { name: "stock_agent.semantic_search_news_events", group: "语义召回", purpose: "基于向量资产语义检索新闻事件；不静默降级为关键词搜索。" },
  { name: "stock_agent.semantic_search_news_threads", group: "消息脉络", purpose: "按真实向量相似度检索可用主题；结果只是关联候选，不代表事实关系或交易结论。" },
  { name: "stock_agent.get_news_thread", group: "消息脉络", purpose: "读取主题当前结论、历史阶段、证据、反证、催化和失效条件。" },
  { name: "stock_agent.list_news_context_changes", group: "消息脉络", purpose: "分页读取一次归纳中的全部变化主题，供周期复核做完整性核对。" },
  { name: "stock_agent.search_news_link_candidates", group: "项目资料查询", purpose: "查询新闻和股票的候选关联记录，辅助发现弱关联或待确认线索。" },
  { name: "stock_agent.list_existing_strategies", group: "策略上下文", purpose: "列出候选标的已有策略，避免重复生成或忽略现有策略约束。" },
  { name: "stock_agent.get_portfolio_context", group: "组合上下文", purpose: "读取组合、持仓和风险上下文；只用于分析，不允许通过 MCP 改仓位。" },
  { name: "stock_agent.get_embedding_status", group: "语义召回", purpose: "检查嵌入模型绑定、维度、向量资产数量和可用状态，是 semantic_search 前置检查。" },
  { name: "stock_agent.start_discovery_step", group: "过程记录", purpose: "标记机会发现某个阶段开始，让前端时间线能显示当前步骤。" },
  { name: "stock_agent.finish_discovery_step", group: "过程记录", purpose: "记录阶段输出摘要和完成状态，形成可恢复的研究轨迹。" },
  { name: "stock_agent.fail_discovery_step", group: "过程记录", purpose: "记录阶段失败原因，任务可保留失败上下文并继续或结束。" },
  { name: "stock_agent.record_external_source", group: "证据记录", purpose: "记录外部公开来源摘要；后端会剥离 URL query 和 fragment，避免敏感参数入库。" },
  { name: "stock_agent.record_evidence", group: "证据记录", purpose: "记录支撑候选的证据，可关联到 step、candidate 和来源。" },
  { name: "stock_agent.record_candidate", group: "候选记录", purpose: "记录经过主数据校验的候选股票或 ETF，包括关系、评分、风险和理由。" },
  { name: "stock_agent.update_candidate", group: "候选记录", purpose: "在研究过程中更新候选评分、排名、理由、风险或状态。" },
  { name: "stock_agent.submit_result", group: "最终回填", purpose: "提交最终结构化结果。主程序会校验 taskID、taskType、schema 和候选 symbol 后再落库。" },
];

const MCP_TOOL_INFO_BY_NAME = new Map(MCP_TOOL_INFO.map((item) => [item.name, item]));

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

  async function openCreateModelDrawer(modelType: "chat" | "embedding") {
    await ensureProvidersLoaded();
    setModelDrawer({ type: "create", modelType });
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
    await Promise.all([
      models ? Promise.resolve(models) : loadModels(),
      providers ? Promise.resolve(providers) : loadProviders(),
    ]);
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
          onCreate={(modelType) => void openCreateModelDrawer(modelType)}
        />
      </CollapsibleSection>

      <CollapsibleSection
        title="任务绑定"
        subtitle="已开放任务可绑定主备模型,未开放任务先置灰展示"
      >
        <AgentTaskProfileSection
          loading={tLoading || taskProfiles === null}
          error={tError}
          models={models ?? []}
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
          initialModelType={modelDrawer.type === "create" ? modelDrawer.modelType : undefined}
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
          providers={providers ?? []}
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
  const [showTools, setShowTools] = useState(false);

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
  const toolCount = status.requiredTools?.length || 0;
  return (
    <>
      <div className="grid gap-2">
        <div className="rounded border border-[var(--line)] bg-[var(--surface)] px-3 py-2 text-xs">
          <div className="flex flex-wrap items-center gap-2">
            <strong className="text-sm">{status.serverName || "stock_agent"}</strong>
            <Pill tone={status.enabled ? "good" : "warn"}>{status.enabled ? "可用" : "未启动"}</Pill>
            <Pill tone="neutral">{status.transport || "loopback_http"}</Pill>
          </div>
          <div className="mt-2 grid gap-1">
            <Row label="Endpoint" value={status.url || "(未启动)"} />
            <Row label="Tools" value={toolCount > 0 ? `${toolCount} 个工具` : "-"} />
          </div>
          {toolCount > 0 ? (
            <div className="mt-2 flex justify-end">
              <Button onClick={() => setShowTools(true)}>
                <Robot size={12} className="mr-1" />
                查看工具
              </Button>
            </div>
          ) : null}
        </div>
        <div className="flex justify-end">
          <Button onClick={() => void onRetry()}>刷新</Button>
        </div>
      </div>
      {showTools ? <MCPToolsDrawer status={status} onClose={() => setShowTools(false)} /> : null}
    </>
  );
}

function MCPToolsDrawer({ status, onClose }: { status: StockV2AgentMCPStatus; onClose: () => void }) {
  const tools = (status.requiredTools || []).map((name) => {
    return MCP_TOOL_INFO_BY_NAME.get(name) || {
      name,
      group: "未分类",
      purpose: "当前前端没有记录该工具说明，请以后端 tools/list 描述为准。",
    };
  });
  return (
    <Drawer
      title="stock_agent MCP 工具"
      subtitle={`${status.transport || "loopback_http"} · ${tools.length} 个工具`}
      onClose={onClose}
      width={760}
    >
      <div className="grid gap-4 text-sm">
        <div className="rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3 text-xs">
          <Row label="Server" value={status.serverName || "stock_agent"} />
          <Row label="Endpoint" value={status.url || "(未启动)"} />
        </div>
        <Notice tone="warn">
          这些 MCP 工具只给 Codex CLI 的股票 Agent 使用：查询项目内资料、记录研究过程、回填结构化结果。外部公开资料搜索仍由 Codex CLI 自己完成。
        </Notice>
        <div className="grid gap-2">
          {tools.map((tool) => (
            <div key={tool.name} className="rounded-lg border border-[var(--line)] bg-[var(--surface)] p-3">
              <div className="flex flex-wrap items-center gap-2">
                <code className="break-all rounded bg-[var(--surface-soft)] px-1.5 py-0.5 font-mono text-xs text-[var(--text)]">
                  {tool.name}
                </code>
                <Pill tone="neutral">{tool.group}</Pill>
              </div>
              <p className="mt-2 mb-0 text-xs leading-relaxed text-[var(--muted-strong)]">{tool.purpose}</p>
            </div>
          ))}
        </div>
      </div>
    </Drawer>
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
              <Pill tone={stockV2AgentAvailabilityTone(p.availability)}>
                {stockV2AgentAvailabilityLabel(p.availability)}
              </Pill>
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

function stockV2AgentModelTypeLabel(modelType?: string): string {
  return modelType === "embedding" ? "嵌入" : "对话";
}

function AgentModelSection({
  loading,
  error,
  items,
  onRetry,
  toggleBusy,
  onToggle,
  onEdit,
  onCreate,
}: {
  loading: boolean;
  error: string | null;
  items: StockV2AgentModelProfile[] | null;
  onRetry: () => void;
  toggleBusy: string | null;
  onToggle: (id: string, enabled: boolean) => void;
  onEdit: (m: StockV2AgentModelProfile) => void;
  onCreate: (modelType: "chat" | "embedding") => void;
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
  const chatModels = (items || []).filter((model) => (model.modelType || "chat") === "chat");
  const embeddingModels = (items || []).filter((model) => model.modelType === "embedding");
  return (
    <div className="grid gap-4">
      <AgentModelGroup
        title="对话模型"
        subtitle="用于 Agent task、CLI debug 和策略生成等对话式执行"
        empty="暂无对话模型。"
        createLabel="新建对话模型"
        models={chatModels}
        modelType="chat"
        toggleBusy={toggleBusy}
        onToggle={onToggle}
        onEdit={onEdit}
        onCreate={onCreate}
      />
      <AgentModelGroup
        title="嵌入模型"
        subtitle="用于后续向量化能力，不参与 Agent task 绑定"
        empty="暂无嵌入模型。"
        createLabel="新建嵌入模型"
        models={embeddingModels}
        modelType="embedding"
        toggleBusy={toggleBusy}
        onToggle={onToggle}
        onEdit={onEdit}
        onCreate={onCreate}
      />
    </div>
  );
}

function AgentModelGroup({
  title,
  subtitle,
  empty,
  createLabel,
  models,
  modelType,
  toggleBusy,
  onToggle,
  onEdit,
  onCreate,
}: {
  title: string;
  subtitle: string;
  empty: string;
  createLabel: string;
  models: StockV2AgentModelProfile[];
  modelType: "chat" | "embedding";
  toggleBusy: string | null;
  onToggle: (id: string, enabled: boolean) => void;
  onEdit: (m: StockV2AgentModelProfile) => void;
  onCreate: (modelType: "chat" | "embedding") => void;
}) {
  return (
    <div className="grid gap-2 border-t border-[var(--line)] pt-3 first:border-t-0 first:pt-0">
      <div className="flex flex-wrap items-start justify-between gap-2">
        <div>
          <div className="flex items-center gap-2">
            <strong className="text-sm">{title}</strong>
            <Pill tone="neutral">{models.length}</Pill>
          </div>
          <p className="mt-0.5 text-xs text-[var(--muted)]">{subtitle}</p>
        </div>
        <Button onClick={() => onCreate(modelType)}>
          <Plus size={14} className="mr-1" /> {createLabel}
        </Button>
      </div>
      {models.length === 0 ? <p className="text-xs text-[var(--muted)]">{empty}</p> : null}
      {models.map((m) => (
        <div key={m.id} className="rounded border border-[var(--line)] bg-[var(--surface)] px-3 py-2 text-xs">
          <div className="flex flex-wrap items-center gap-2">
            <strong className="text-sm">{m.displayName || m.modelName}</strong>
            <span className="font-mono text-[var(--muted-strong)]">{m.modelName}</span>
            <Pill tone="neutral">{stockV2AgentModelTypeLabel(m.modelType || "chat")}</Pill>
            <Pill tone={stockV2AgentModelStatusTone(m.status)}>{stockV2AgentModelStatusLabel(m.status)}</Pill>
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
            {modelType === "embedding" ? ` · ${m.embeddingProtocol || "openai_embeddings"}` : ""}
            {modelType === "embedding" && m.embeddingDimensions ? ` · ${m.embeddingDimensions} 维` : ""}
            {modelType === "chat" && m.contextLimit ? ` · 上下文 ${m.contextLimit}` : ""}
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
  models,
  profiles,
  onRetry,
  onEdit,
}: {
  loading: boolean;
  error: string | null;
  models: StockV2AgentModelProfile[];
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
  const modelByID = new Map(models.map((item) => [item.id, item]));
  const bindingLabel = (modelID?: string) => {
    if (!modelID) return "(未绑定)";
    const model = modelByID.get(modelID);
    if (!model) return `${modelID.slice(0, 12)} (模型不存在)`;
    const status = model.enabled ? stockV2AgentModelStatusLabel(model.status) : "未启用";
    return `${model.displayName || model.modelName} (${status})`;
  };
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
                <Row label="执行模式" value={profile?.executionMode === "api" ? "API" : "CLI"} />
                <Row label="主模型" value={bindingLabel(profile?.primaryModelId)} />
                <Row label="备模型" value={bindingLabel(profile?.fallbackModelId)} />
                <Row label="推理强度" value={profile?.reasoningEffort || "模型默认（不传）"} />
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
  const chatModels = models.filter((model) => (model.modelType || "chat") === "chat");
  const usableModels = chatModels.filter((model) => model.enabled && model.status === "available");
  const [modelId, setModelId] = useState(usableModels[0]?.id || chatModels[0]?.id || "");
  const [submitting, setSubmitting] = useState(false);
  const [result, setResult] = useState<StockV2AgentExecutionDetail | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [debugStartedAt, setDebugStartedAt] = useState<number | null>(null);
  const [elapsedSeconds, setElapsedSeconds] = useState(0);
  const activeRun = result && agentRunStillActive(result.run.status) ? result.run : null;
  const debugActive = submitting || !!activeRun;

  useEffect(() => {
    if (!debugActive || !debugStartedAt) return;
    const tick = () => setElapsedSeconds(Math.max(0, Math.floor((Date.now() - debugStartedAt) / 1000)));
    tick();
    const timer = window.setInterval(tick, 1000);
    return () => window.clearInterval(timer);
  }, [debugActive, debugStartedAt]);

  useEffect(() => {
    if (!activeRun?.id) return;
    let mounted = true;
    const timer = window.setInterval(() => {
      void actions.api<StockV2AgentExecutionDetail>(`/api/stockv2/agent/runs/${encodeURIComponent(activeRun.id)}/detail`)
        .then((next) => {
          if (!mounted) return;
          setResult(next);
          if (!agentRunStillActive(next.run.status)) {
            actions.setToast(next.run.status === "completed" ? "CLI 验证完成" : "CLI 验证失败，已保存执行上下文", next.run.status === "completed" ? "good" : "warn");
          }
        })
        .catch((err) => {
          if (!mounted) return;
          setError(friendlyError(err));
        });
    }, 2000);
    return () => {
      mounted = false;
      window.clearInterval(timer);
    };
  }, [actions, activeRun?.id]);

  async function runDebug() {
    if (!modelId) return;
    setSubmitting(true);
    setError(null);
    setResult(null);
    setDebugStartedAt(Date.now());
    setElapsedSeconds(0);
    try {
      const body: StockV2AgentRunCLIDebugRequest = { modelId, async: true };
      const res = await actions.api<StockV2AgentExecutionDetail>("/api/stockv2/agent/cli-debug", {
        method: "POST",
        body,
      });
      setResult(res);
      actions.setToast(agentRunStillActive(res.run.status) ? "CLI 验证已启动" : "CLI 验证已返回上下文", agentRunStillActive(res.run.status) ? "good" : res.run.status === "completed" ? "good" : "warn");
    } catch (err) {
      setError(friendlyError(err));
      actions.setToast(friendlyError(err), "danger");
    } finally {
      setSubmitting(false);
    }
  }

  async function reloadModels() {
    const nextItems = await onReloadModels();
    const nextChatModels = nextItems.filter((model) => (model.modelType || "chat") === "chat");
    const first = nextChatModels.find((model) => model.enabled && model.status === "available")?.id || nextChatModels[0]?.id || "";
    setModelId((current) => current || first);
  }

  return (
    <Drawer title="验证 CLI" subtitle="运行一次 Codex CLI + MCP submit_result 调试任务" onClose={onClose} width={720}>
      <div className="grid gap-4">
        <Field label="模型">
          <select value={modelId} onChange={(event) => setModelId(event.target.value)}>
            {chatModels.length === 0 ? <option value="">暂无对话模型</option> : null}
            {chatModels.map((model) => (
              <option key={model.id} value={model.id}>
                {model.displayName || model.modelName}{model.enabled && model.status === "available" ? "" : " (不可用于任务)"}
              </option>
            ))}
          </select>
        </Field>
        <p className="text-xs leading-relaxed text-[var(--muted)]">
          这会启动一次真实 Codex CLI 调试任务,并要求 CLI 通过股票模块 MCP 回填结果。完成后下方会显示 stdout/stderr 摘要和 MCP 结构化结果。
        </p>
        {debugStartedAt ? (
          <div className="grid gap-2 rounded-lg border border-[var(--line)] bg-[var(--surface-soft)] p-3 text-xs">
            <div className="flex flex-wrap items-center gap-2">
              <span className="font-semibold text-[var(--muted-strong)]">调试状态</span>
              <Pill tone={error && !result ? "danger" : result ? stockV2AgentRunStatusTone(result.run.status) : "warn"}>
                {error && !result ? "请求失败" : submitting && !result ? "请求中" : stockV2AgentRunStatusLabel(result?.run.status)}
              </Pill>
              <span className="font-mono text-[var(--muted)]">{formatElapsedSeconds(elapsedSeconds)}</span>
            </div>
            <Row label="当前阶段" value={cliDebugProgressText(submitting, result, elapsedSeconds)} />
            <Row label="Run ID" value={result?.run.id || "等待后端创建"} />
            <Row label="查看位置" value="Agent 执行台账会保留 stdout/stderr、MCP 回填和失败原因" />
          </div>
        ) : null}
        {error ? <Notice tone="danger">{error}</Notice> : null}
        <div className="flex justify-end gap-2 border-t border-[var(--line)] pt-3">
          <Button onClick={() => void reloadModels()} disabled={submitting}>刷新模型</Button>
          <Button onClick={onClose}>关闭</Button>
          <Button tone="primary" disabled={debugActive || !modelId} onClick={() => void runDebug()}>
            {debugActive ? "调试中…" : "开始调试"}
          </Button>
        </div>
        {result ? <StockV2AgentRunDetailPanel detail={result} /> : null}
      </div>
    </Drawer>
  );
}

// ===== 小工具 =====

function agentRunStillActive(status?: string): boolean {
  return status === "pending" || status === "ready" || status === "running";
}

function formatElapsedSeconds(seconds: number): string {
  if (seconds < 60) return `${seconds}s`;
  const minutes = Math.floor(seconds / 60);
  const rest = seconds % 60;
  return `${minutes}m ${String(rest).padStart(2, "0")}s`;
}

function cliDebugProgressText(submitting: boolean, detail: StockV2AgentExecutionDetail | null, elapsedSeconds: number): string {
  if (submitting && !detail) return "请求已发出，等待后端创建 AgentRun";
  const status = detail?.run.status;
  if (status === "ready" || status === "pending") return "AgentRun 已创建，等待后台执行器启动";
  if (status === "running") {
    if (elapsedSeconds > 45) return "Codex CLI 仍在运行或等待 MCP submit_result 回填";
    if (elapsedSeconds > 10) return "Codex CLI 已启动，正在等待 stdout/stderr 或 MCP 回填";
    return "后台执行器正在启动 Codex CLI";
  }
  if (status === "completed") return "已收到 MCP 回填并写入执行台账";
  if (status === "failed") return "调试失败，详情里会保留错误和 stdout/stderr 摘要";
  return "尚未开始";
}

function Row({ label, value }: { label: string; value: string }) {
  return (
    <div className="grid grid-cols-[72px_minmax(0,1fr)] gap-2">
      <span className="text-[var(--muted)]">{label}</span>
      <span className="break-words text-[var(--muted-strong)]">{value}</span>
    </div>
  );
}
